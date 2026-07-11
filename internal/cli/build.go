package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

type buildOpts struct {
	workspaceFolder            string
	configPath                 string
	dockerPath                 string
	logLevel                   string
	logFormat                  string
	noCache                    bool
	imageNames                 []string
	cacheFrom                  []string
	cacheTo                    string
	buildkit                   string
	platform                   string
	push                       bool
	labels                     []string
	output                     string
	additionalFeatures         string
	skipFeatureAutoMapping     bool
	skipPersistCustoms         bool
	experimentalLockfile       bool
	experimentalFrozenLockfile bool
	// lockfileExcludeIDs is populated after merging --additional-features; it lists
	// userFeatureIds supplied only via that flag, which 0.88 keeps out of the lockfile.
	lockfileExcludeIDs map[string]bool
}

func newBuildCmd() *cobra.Command {
	var opts buildOpts

	cmd := &cobra.Command{
		Use:   "build [path]",
		Short: "Build a dev container image",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd.Context(), &opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")
	f.BoolVar(&opts.noCache, "no-cache", false, "Build with --no-cache.")
	f.StringArrayVar(&opts.imageNames, "image-name", nil, "Image name(s).")
	f.StringArrayVar(&opts.cacheFrom, "cache-from", nil, "Cache from images.")
	f.StringVar(&opts.cacheTo, "cache-to", "", "Cache to destination.")
	f.StringVar(&opts.buildkit, "buildkit", "auto", "BuildKit mode.")
	f.StringVar(&opts.platform, "platform", "", "Target platform.")
	f.BoolVar(&opts.push, "push", false, "Push to registry.")
	f.StringArrayVar(&opts.labels, "label", nil, "Image labels.")
	f.StringVar(&opts.output, "output", "", "Build output.")
	f.String("docker-compose-path", "", "Docker Compose CLI path.")
	f.StringVar(&opts.additionalFeatures, "additional-features", "", "Additional features JSON.")
	f.String("user-data-folder", "", "User data folder.")
	f.BoolVar(&opts.skipFeatureAutoMapping, "skip-feature-auto-mapping", false, "")
	f.BoolVar(&opts.skipPersistCustoms, "skip-persisting-customizations-from-features", false, "")
	f.BoolVar(&opts.experimentalLockfile, "experimental-lockfile", false, "")
	f.BoolVar(&opts.experimentalFrozenLockfile, "experimental-frozen-lockfile", false, "")
	f.Bool("omit-syntax-directive", false, "")
	cmd.Flags().MarkHidden("skip-feature-auto-mapping")
	cmd.Flags().MarkHidden("skip-persisting-customizations-from-features")
	cmd.Flags().MarkHidden("experimental-lockfile")
	cmd.Flags().MarkHidden("experimental-frozen-lockfile")
	cmd.Flags().MarkHidden("omit-syntax-directive")

	return cmd
}

// buildRunner carries the dependencies shared across the `build` flow so they
// don't have to be threaded through every helper by hand.
type buildRunner struct {
	ctx    context.Context
	log    log.Log
	docker *docker.Client
	engine *docker.EngineClient
	opts   *buildOpts
}

func runBuild(ctx context.Context, opts *buildOpts) error {
	// 0.88: --workspace-folder defaults to the current directory when not given.
	if opts.workspaceFolder == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}
	for _, v := range []struct {
		flag, val string
		choices   []string
	}{
		{"log-level", opts.logLevel, []string{"info", "debug", "trace"}},
		{"log-format", opts.logFormat, []string{"text", "json"}},
		{"buildkit", opts.buildkit, []string{"auto", "never"}},
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return writeValidationError(err.Error())
		}
	}

	// Resolve paths
	workspaceFolder := resolvePath(opts.workspaceFolder)
	configPath := ""
	if opts.configPath != "" {
		configPath = resolvePath(opts.configPath)
	}

	// Setup logger
	logger := log.New(log.Options{
		Version: cliVersion(),
		Level:   log.MapLogLevel(opts.logLevel),
		Format:  opts.logFormat,
		Writer:  os.Stderr,
	})

	// Load config
	loadResult, err := config.LoadDevContainerConfig(workspaceFolder, configPath, "")
	if err != nil {
		return writeErrorResult(err.Error())
	}
	cfg := loadResult.Config

	opts.lockfileExcludeIDs, err = mergeAdditionalFeatures(cfg, opts.additionalFeatures)
	if err != nil {
		return writeErrorResult(err.Error())
	}

	// Setup Docker clients
	dockerClient := docker.NewClient(opts.dockerPath, nil, logger)

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	run := &buildRunner{ctx: ctx, log: logger, docker: dockerClient, engine: engine, opts: opts}

	// Detect BuildKit
	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit()
		useBuildx = bk.Available
	}

	// Validate buildx-only flags
	if (opts.platform != "" || opts.push) && !useBuildx {
		return writeErrorResult("--platform or --push require BuildKit enabled.")
	}
	if opts.output != "" && opts.push {
		return writeErrorResult("--push true cannot be used with --output.")
	}

	// Validate compose-incompatible flags
	if cfg.IsComposeConfig() {
		if opts.platform != "" || opts.push {
			return writeErrorResult("--platform or --push not supported.")
		}
		if opts.output != "" {
			return writeErrorResult("--output not supported.")
		}
		if opts.cacheTo != "" {
			return writeErrorResult("--cache-to not supported.")
		}
	}

	var imageNameResult []string

	if cfg.IsDockerfileConfig() {
		imageNameResult, err = run.buildDockerfile(cfg, loadResult, useBuildx)
	} else if cfg.IsComposeConfig() {
		imageNameResult, err = run.buildCompose(cfg, useBuildx)
	} else {
		// Image-based config
		imageNameResult, err = run.buildImage(cfg, loadResult, useBuildx)
	}

	if err != nil {
		if isUserFacingBuildError(err) {
			return writeErrorResult(err.Error())
		}
		return writeErrorJSON(coreerrors.ToErrorOutput(&coreerrors.ContainerError{
			Description: fmt.Sprintf("An error occurred building the container: %v", err),
		}))
	}

	return writeSuccessJSON(map[string]interface{}{
		"outcome":   "success",
		"imageName": imageNameResult,
	})
}

func (r *buildRunner) buildDockerfile(cfg *config.DevContainerConfig, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	ctx, logger, dockerClient, engine, opts := r.ctx, r.log, r.docker, r.engine, r.opts
	// Read Dockerfile
	dockerfilePath := cfg.GetDockerfile()
	if dockerfilePath == "" {
		return nil, fmt.Errorf("no Dockerfile specified")
	}

	// Resolve relative to config dir
	configDir := filepath.Dir(cfg.ConfigFilePath)
	dockerfilePath = filepath.Join(configDir, dockerfilePath)

	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile: %w", err)
	}

	// Parse Dockerfile
	df := docker.ExtractDockerfile(string(content))

	// Determine build target: use config target if set, otherwise ensure final stage has a name
	stageName := ""
	if cfg.Build != nil && cfg.Build.Target != "" {
		stageName = cfg.Build.Target
	} else {
		var modifiedContent string
		stageName, modifiedContent = docker.EnsureDockerfileHasFinalStageName(string(content), "dev_container_auto_added_stage_label")
		if modifiedContent != "" {
			// Write modified Dockerfile to temp location
			tmpDockerfile := dockerfilePath + ".devcontainer.build"
			if err := os.WriteFile(tmpDockerfile, []byte(modifiedContent), 0644); err != nil {
				return nil, fmt.Errorf("write modified Dockerfile: %w", err)
			}
			defer os.Remove(tmpDockerfile)
			dockerfilePath = tmpDockerfile
		}
	}

	baseImage := docker.FindBaseImage(df, buildArgsFromConfig(cfg), stageName)
	logger.Write(fmt.Sprintf("Base image: %s", baseImage), log.LevelInfo)

	// Generate base-image metadata. Ensure the base image is available locally so
	// metadata from the published image label is preserved.
	var baseLabels map[string]string
	if baseInspect, inspErr := engine.InspectImage(ctx, baseImage); inspErr == nil && baseInspect.Config != nil {
		baseLabels = baseInspect.Config.Labels
	} else if pullErr := engine.PullImage(ctx, baseImage); pullErr == nil {
		if baseInspect, inspErr := engine.InspectImage(ctx, baseImage); inspErr == nil && baseInspect.Config != nil {
			baseLabels = baseInspect.Config.Labels
		}
	}
	baseMetadata := imagemeta.ReadMetadataFromLabels(baseLabels, logger)
	metadata := append([]imagemeta.Entry{}, baseMetadata...)
	if len(cfg.Features) == 0 {
		metadata = append(metadata, configToMetadataEntry(cfg))
	}
	metadataLabel := imagemeta.GenerateMetadataLabel(metadata)

	// Determine image names (match TS getFolderImageName: vsc-{basename}-{hash})
	imageNames := opts.imageNames
	if len(imageNames) == 0 {
		imageNames = []string{folderImageName(resolvePath(opts.workspaceFolder))}
	}

	// Build context
	contextPath := configDir
	if cfg.GetBuildContext() != "" {
		contextPath = filepath.Join(configDir, cfg.GetBuildContext())
	}

	// Build args from config
	buildArgs := buildArgsFromConfig(cfg)

	// Add metadata label
	allLabels := append(opts.labels, fmt.Sprintf("%s=%s", imagemeta.MetadataLabel, metadataLabel))

	// When features are present the base image is an intermediate build: it must
	// land in the local image store so the feature build can reference it as
	// $_DEV_CONTAINERS_BASE_IMAGE. Defer --output/--push to the feature build
	// (below) — applying them here would export the base to a tarball/registry
	// without loading it, breaking the feature stage.
	hasFeatures := len(cfg.Features) > 0
	baseOutput, basePush := opts.output, opts.push
	if hasFeatures {
		baseOutput, basePush = "", false
	}

	buildOpts := docker.BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: contextPath,
		Tags:        imageNames,
		Target:      stageName,
		BuildArgs:   buildArgs,
		CacheFrom:   opts.cacheFrom,
		Labels:      allLabels,
		NoCache:     opts.noCache,
		ExtraArgs:   buildOptionsFromConfig(cfg),
		UseBuildx:   useBuildx,
		Platform:    opts.platform,
		Push:        basePush,
		Output:      baseOutput,
		CacheTo:     opts.cacheTo,
	}

	result, err := dockerClient.Build(buildOpts)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf(msgDockerBuildFailed, result.ExitCode, string(result.Stderr))
	}

	logger.Write(string(result.Stderr), log.LevelInfo)

	// If config has features, extend the built image with features
	if len(cfg.Features) > 0 {
		baseImageName := imageNames[0]
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, baseImageName, cfg.Features, useBuildx, imageNames, &FeatureBuildOptions{
			NoCache:                opts.noCache,
			CacheFrom:              opts.cacheFrom,
			CacheTo:                opts.cacheTo,
			Labels:                 opts.labels,
			Platform:               opts.platform,
			Push:                   opts.push,
			Output:                 opts.output,
			ContainerEnv:           cfg.ContainerEnv,
			SkipFeatureAutoMapping: opts.skipFeatureAutoMapping,
			Lockfile:               opts.experimentalLockfile,
			FrozenLockfile:         opts.experimentalFrozenLockfile,
			ConfigPath:             cfg.ConfigFilePath,
			LockfileExcludeIDs:     opts.lockfileExcludeIDs,
			SkipPersistCustoms:     opts.skipPersistCustoms,
			FeaturesBasePath:       filepath.Dir(cfg.ConfigFilePath),
			AdditionalMetadata:     []imagemeta.Entry{configToMetadataEntry(cfg)},
		})
	}

	return imageNames, nil
}

func (r *buildRunner) buildImage(cfg *config.DevContainerConfig, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	ctx, logger, dockerClient, engine, opts := r.ctx, r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return nil, fmt.Errorf("no image specified in devcontainer.json")
	}

	// Pre-validate features before pulling the image, so local feature errors
	// surface immediately instead of after an expensive pull.
	if len(cfg.Features) > 0 {
		for id := range cfg.Features {
			srcType := features.ClassifyFeatureID(id)
			if srcType == features.SourceLegacyShorthand {
				name := id
				if idx := strings.LastIndex(id, ":"); idx > 0 {
					name = id[:idx]
				}
				if !features.IsKnownLegacyFeature(name) {
					return nil, fmt.Errorf(msgLegacyFeature, id)
				}
			}
		}
	}

	logger.Write(fmt.Sprintf("Using image: %s", cfg.Image), log.LevelInfo)

	imageNames := opts.imageNames
	if len(imageNames) == 0 {
		imageNames = []string{folderImageName(resolvePath(opts.workspaceFolder))}
	}

	// Ensure image exists locally
	_, err := engine.InspectImage(ctx, cfg.Image)
	if err != nil {
		logger.Write(fmt.Sprintf("Pulling image %s...", cfg.Image), log.LevelInfo)
		if pullErr := engine.PullImage(ctx, cfg.Image); pullErr != nil {
			return nil, fmt.Errorf("image %q not found locally and pull failed: %w", cfg.Image, pullErr)
		}
	}

	if len(cfg.Features) > 0 {
		featureImageNames := opts.imageNames
		if len(featureImageNames) == 0 {
			featureImageNames = []string{folderImageName(resolvePath(opts.workspaceFolder)) + "-features"}
		}
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, featureImageNames, &FeatureBuildOptions{
			NoCache:                opts.noCache,
			CacheFrom:              opts.cacheFrom,
			CacheTo:                opts.cacheTo,
			Labels:                 opts.labels,
			Platform:               opts.platform,
			Push:                   opts.push,
			Output:                 opts.output,
			ContainerEnv:           cfg.ContainerEnv,
			SkipFeatureAutoMapping: opts.skipFeatureAutoMapping,
			Lockfile:               opts.experimentalLockfile,
			FrozenLockfile:         opts.experimentalFrozenLockfile,
			ConfigPath:             cfg.ConfigFilePath,
			LockfileExcludeIDs:     opts.lockfileExcludeIDs,
			SkipPersistCustoms:     opts.skipPersistCustoms,
			FeaturesBasePath:       filepath.Dir(cfg.ConfigFilePath),
		})
	}

	// When --platform, --push, or --output is specified, route through the feature
	// extension path to generate a proper Dockerfile for buildx. When features exist,
	// extendImageWithFeatures handles everything. When no features, build a minimal
	// Dockerfile directly (extendImageWithFeatures returns early with 0 features).
	if opts.platform != "" || opts.push || opts.output != "" {
		featureImageNames := opts.imageNames
		if len(featureImageNames) == 0 {
			featureImageNames = []string{folderImageName(resolvePath(opts.workspaceFolder)) + "-features"}
		}

		if len(cfg.Features) > 0 {
			return extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, featureImageNames, &FeatureBuildOptions{
				NoCache:                opts.noCache,
				CacheFrom:              opts.cacheFrom,
				CacheTo:                opts.cacheTo,
				Labels:                 opts.labels,
				Platform:               opts.platform,
				Push:                   opts.push,
				Output:                 opts.output,
				ContainerEnv:           cfg.ContainerEnv,
				SkipFeatureAutoMapping: opts.skipFeatureAutoMapping,
				Lockfile:               opts.experimentalLockfile,
				FrozenLockfile:         opts.experimentalFrozenLockfile,
				ConfigPath:             cfg.ConfigFilePath,
				LockfileExcludeIDs:     opts.lockfileExcludeIDs,
				SkipPersistCustoms:     opts.skipPersistCustoms,
				FeaturesBasePath:       filepath.Dir(cfg.ConfigFilePath),
			})
		}

		// 0 features + platform/push: build a minimal FROM Dockerfile with config metadata.
		metadata := []imagemeta.Entry{configToMetadataEntry(cfg)}
		metadataLabel := imagemeta.GenerateMetadataLabel(metadata)
		allLabels := append(opts.labels, fmt.Sprintf("%s=%s", imagemeta.MetadataLabel, metadataLabel))

		tmpDir, tmpErr := os.MkdirTemp("", "devcontainer-image-build-")
		if tmpErr != nil {
			return nil, fmt.Errorf("create temp dir: %w", tmpErr)
		}
		defer os.RemoveAll(tmpDir)
		dfPath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dfPath, []byte(fmt.Sprintf("FROM %s\n", cfg.Image)), 0644); err != nil {
			return nil, fmt.Errorf("write temp Dockerfile: %w", err)
		}

		result, buildErr := dockerClient.Build(docker.BuildOptions{
			Dockerfile:  dfPath,
			ContextPath: tmpDir,
			Tags:        featureImageNames,
			Labels:      allLabels,
			UseBuildx:   useBuildx,
			Platform:    opts.platform,
			Push:        opts.push,
			Output:      opts.output,
			CacheFrom:   opts.cacheFrom,
			CacheTo:     opts.cacheTo,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("build image: %w", buildErr)
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf(msgDockerBuildFailed, result.ExitCode, string(result.Stderr))
		}
		logger.Write(string(result.Stderr), log.LevelInfo)
		return featureImageNames, nil
	}

	// No platform/push — use temp Dockerfile when imageNames require labeling
	if len(opts.imageNames) > 0 {
		metadata := []imagemeta.Entry{configToMetadataEntry(cfg)}
		metadataLabel := imagemeta.GenerateMetadataLabel(metadata)
		allLabels := append(opts.labels, fmt.Sprintf("%s=%s", imagemeta.MetadataLabel, metadataLabel))

		tmpDir, tmpErr := os.MkdirTemp("", "devcontainer-image-build-")
		if tmpErr != nil {
			return nil, fmt.Errorf("create temp dir: %w", tmpErr)
		}
		defer os.RemoveAll(tmpDir)
		dfPath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dfPath, []byte(fmt.Sprintf("FROM %s\n", cfg.Image)), 0644); err != nil {
			return nil, fmt.Errorf("write temp Dockerfile: %w", err)
		}

		result, buildErr := dockerClient.Build(docker.BuildOptions{
			Dockerfile:  dfPath,
			ContextPath: tmpDir,
			Tags:        imageNames,
			Labels:      allLabels,
			UseBuildx:   useBuildx,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("build image: %w", buildErr)
		}
		if result.ExitCode != 0 {
			for _, name := range opts.imageNames {
				if tagErr := dockerClient.Tag(cfg.Image, name); tagErr != nil {
					return nil, fmt.Errorf("failed to build and tag image %q: %w", name, tagErr)
				}
			}
		}
		return imageNames, nil
	}

	// No image names, no platform/push — return base image as-is

	return imageNames, nil
}

func buildArgsFromConfig(cfg *config.DevContainerConfig) map[string]string {
	if cfg.Build == nil || cfg.Build.Args == nil {
		return map[string]string{}
	}
	return cfg.Build.Args
}

func dockerfileBuildMetadataEntries(cfg *config.DevContainerConfig, baseLabels map[string]string, logger log.Log) []imagemeta.Entry {
	metadata := []imagemeta.Entry{}
	if len(baseLabels) > 0 {
		metadata = append(metadata, imagemeta.ReadMetadataFromLabels(baseLabels, logger)...)
	}
	metadata = append(metadata, configToMetadataEntry(cfg))
	return metadata
}

// --- Output helpers ---

func resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, p)
}

func buildOptionsFromConfig(cfg *config.DevContainerConfig) []string {
	if cfg.Build == nil || len(cfg.Build.Options) == 0 {
		return nil
	}
	return cfg.Build.Options
}

func writeSuccessJSON(data map[string]interface{}) error {
	out, _ := json.Marshal(data)
	fmt.Fprintln(os.Stdout, string(out))
	return nil
}

func writeErrorResult(description string) error {
	return writeErrorJSON(coreerrors.ErrorOutput{
		Outcome:     "error",
		Message:     description,
		Description: description,
	})
}

// writeValidationError reports a CLI argument/usage error the way yargs does in
// the TS CLI: the reason is printed to stderr and stdout is left empty (no JSON
// envelope). Reserved for the validation phase (flag parsing, enum checks,
// required args, format/implication checks) that runs before any runtime work.
// Runtime failures keep using writeErrorResult (JSON envelope on stdout).
func writeValidationError(message string) error {
	fmt.Fprintln(os.Stderr, message)
	return &coreerrors.ExitCodeError{Code: 1, Err: fmt.Errorf("%s", message)}
}

func writeErrorJSON(errOutput coreerrors.ErrorOutput) error {
	out, _ := json.Marshal(errOutput)
	fmt.Fprintln(os.Stdout, string(out))
	return &coreerrors.ExitCodeError{Code: 1, Err: fmt.Errorf("%s", errOutput.Description)}
}

func isUserFacingBuildError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, legacyFeaturePrefix)
}

func (r *buildRunner) buildCompose(cfg *config.DevContainerConfig, useBuildx bool) ([]string, error) {
	ctx, logger, dockerClient, engine, opts := r.ctx, r.log, r.docker, r.engine, r.opts
	if cfg.Service == "" {
		return nil, fmt.Errorf("dockerComposeFile config requires 'service' property")
	}

	env := osEnvMap()
	composeFiles, err := config.GetDockerComposeFilePaths(cfg, env, filepath.Dir(cfg.ConfigFilePath))
	if err != nil {
		return nil, fmt.Errorf("resolve compose files: %w", err)
	}

	composeClient, err := docker.NewComposeClient(opts.dockerPath, "", nil, logger)
	if err != nil {
		return nil, fmt.Errorf("compose client: %w", err)
	}

	composeConfig, err := composeClient.Config(composeFiles, "")
	if err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	serviceConfig := composeServiceConfig(composeConfig, cfg.Service)

	// Compute project name BEFORE build so compose image names are deterministic
	projectName := docker.ToProjectName(
		filepath.Base(resolvePath(opts.workspaceFolder))+"_devcontainer",
		composeClient.UsesNewProjectNames(),
	)

	baseImageName := fmt.Sprintf("%s-%s", projectName, cfg.Service)
	if imageName, ok := serviceConfig["image"].(string); ok && imageName != "" {
		baseImageName = imageName
	}
	_, hasBuild := serviceConfig["build"]

	if hasBuild {
		logger.Write(fmt.Sprintf("Building compose service %s...", cfg.Service), log.LevelInfo)
		buildErr := composeClient.Build(composeFiles, "", []string{"--project-name", projectName}, []string{cfg.Service}, opts.noCache)
		if buildErr != nil {
			return nil, fmt.Errorf("compose build: %w", buildErr)
		}
	}

	if len(cfg.Features) > 0 {
		if !hasBuild {
			if _, err := engine.InspectImage(ctx, baseImageName); err != nil {
				logger.Write(fmt.Sprintf("Pulling image %s...", baseImageName), log.LevelInfo)
				if pullErr := engine.PullImage(ctx, baseImageName); pullErr != nil {
					return nil, fmt.Errorf("failed to pull image %q: %w", baseImageName, pullErr)
				}
			}
		}
		imageNames := opts.imageNames
		if len(imageNames) == 0 {
			imageNames = []string{baseImageName + "-features"}
		}
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, baseImageName, cfg.Features, useBuildx, imageNames, &FeatureBuildOptions{
			NoCache:                opts.noCache,
			CacheFrom:              opts.cacheFrom,
			CacheTo:                opts.cacheTo,
			Labels:                 opts.labels,
			Platform:               opts.platform,
			Push:                   opts.push,
			Output:                 opts.output,
			ContainerEnv:           cfg.ContainerEnv,
			SkipFeatureAutoMapping: opts.skipFeatureAutoMapping,
			Lockfile:               opts.experimentalLockfile,
			FrozenLockfile:         opts.experimentalFrozenLockfile,
			ConfigPath:             cfg.ConfigFilePath,
			LockfileExcludeIDs:     opts.lockfileExcludeIDs,
			SkipPersistCustoms:     opts.skipPersistCustoms,
			FeaturesBasePath:       filepath.Dir(cfg.ConfigFilePath),
		})
	}

	imageName := baseImageName

	if len(opts.imageNames) > 0 {
		metadata := []imagemeta.Entry{configToMetadataEntry(cfg)}
		metadataLabel := imagemeta.GenerateMetadataLabel(metadata)
		allLabels := append(opts.labels, fmt.Sprintf("%s=%s", imagemeta.MetadataLabel, metadataLabel))

		tmpDir, tmpErr := os.MkdirTemp("", "devcontainer-compose-image-build-")
		if tmpErr != nil {
			return nil, fmt.Errorf("create temp dir: %w", tmpErr)
		}
		defer os.RemoveAll(tmpDir)
		dfPath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dfPath, []byte(fmt.Sprintf("FROM %s\n", imageName)), 0644); err != nil {
			return nil, fmt.Errorf("write temp Dockerfile: %w", err)
		}

		result, buildErr := dockerClient.Build(docker.BuildOptions{
			Dockerfile:  dfPath,
			ContextPath: tmpDir,
			Tags:        opts.imageNames,
			Labels:      allLabels,
			UseBuildx:   useBuildx,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("build image: %w", buildErr)
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf(msgDockerBuildFailed, result.ExitCode, string(result.Stderr))
		}
		return opts.imageNames, nil
	}

	return []string{imageName}, nil
}

func composeServiceConfig(composeConfig map[string]interface{}, service string) map[string]interface{} {
	services, ok := composeConfig["services"].(map[string]interface{})
	if !ok {
		return nil
	}
	svc, ok := services[service].(map[string]interface{})
	if !ok {
		return nil
	}
	return svc
}
