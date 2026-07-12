package cli

import (
	"context"
	"encoding/json"
	"errors"
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
	dockerComposePath          string
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
	noLockfile                 bool
	frozenLockfile             bool
	secretsFile                string
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
			return runBuild(cmd.Context(), outputFor(cmd), &opts)
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
	f.StringVar(&opts.dockerComposePath, "docker-compose-path", "", "Docker Compose CLI path.")
	f.StringVar(&opts.additionalFeatures, "additional-features", "", "Additional features JSON.")
	f.String("user-data-folder", "", "User data folder.")
	f.BoolVar(&opts.skipFeatureAutoMapping, "skip-feature-auto-mapping", false, "")
	f.BoolVar(&opts.skipPersistCustoms, "skip-persisting-customizations-from-features", false, "")
	f.BoolVar(&opts.experimentalLockfile, "experimental-lockfile", false, "")
	f.BoolVar(&opts.experimentalFrozenLockfile, "experimental-frozen-lockfile", false, "")
	f.BoolVar(&opts.noLockfile, "no-lockfile", false, "Disable lockfile generation and verification.")
	f.BoolVar(&opts.frozenLockfile, "frozen-lockfile", false, "Ensure lockfile exists and remains unchanged; fail otherwise.")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "Path to a JSON file with build secrets ({\"KEY\":\"VALUE\"}); each is passed to BuildKit as a build secret (requires buildx).")
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
	log    log.Logger
	docker *docker.Client
	engine *docker.EngineClient
	opts   *buildOpts
}

func runBuild(ctx context.Context, out Output, opts *buildOpts) error {
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
			return writeValidationError(out, err.Error())
		}
	}

	// --no-lockfile is mutually exclusive with every frozen/write lockfile flag
	// (TS devContainersSpecCLI check for the build command).
	if opts.noLockfile {
		switch {
		case opts.frozenLockfile:
			return writeValidationError(out, "--no-lockfile and --frozen-lockfile are mutually exclusive.")
		case opts.experimentalFrozenLockfile:
			return writeValidationError(out, "--no-lockfile and --experimental-frozen-lockfile are mutually exclusive.")
		case opts.experimentalLockfile:
			return writeValidationError(out, "--no-lockfile and --experimental-lockfile are mutually exclusive.")
		}
	}

	// Non-blocking hint (interactive TTY only) if the host was never checked or a
	// previous `devcontainer check` found a failing configuration.
	warnUncheckedHost(out)

	// TS folds the deprecated --experimental-frozen-lockfile into --frozen-lockfile
	// (effectiveFrozenLockfile = frozenLockfile || experimentalFrozenLockfile).
	opts.experimentalFrozenLockfile = opts.experimentalFrozenLockfile || opts.frozenLockfile

	// Resolve paths
	workspaceFolder := resolvePath(opts.workspaceFolder)
	configPath := ""
	if opts.configPath != "" {
		configPath = resolvePath(opts.configPath)
	}

	// Setup logger
	logger := log.New(log.Options{
		Version: cliVersion(),
		Level:   log.ParseLevel(opts.logLevel),
		Format:  opts.logFormat,
		Writer:  os.Stderr,
	})

	// Load config
	loadResult, err := config.LoadDevContainerConfig(workspaceFolder, configPath, "")
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	cfg := loadResult.Config

	opts.lockfileExcludeIDs, err = mergeAdditionalFeatures(cfg, opts.additionalFeatures)
	if err != nil {
		return writeErrorResult(out, err.Error())
	}

	if derr := enforceDisallowedFeatures(ctx, cfg, logger); derr != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(derr))
	}

	// Setup Docker clients
	dockerClient := docker.NewClient(opts.dockerPath, nil, logger)

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	run := &buildRunner{log: logger, docker: dockerClient, engine: engine, opts: opts}

	// Detect BuildKit
	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit(ctx)
		useBuildx = bk.Available
	}

	// Validate buildx-only flags
	if (opts.platform != "" || opts.push) && !useBuildx {
		return writeErrorResult(out, "--platform or --push require BuildKit enabled.")
	}
	if opts.output != "" && opts.push {
		return writeErrorResult(out, "--push true cannot be used with --output.")
	}

	// Validate compose-incompatible flags
	if cfg.IsComposeConfig() {
		if opts.platform != "" || opts.push {
			return writeErrorResult(out, "--platform or --push not supported.")
		}
		if opts.output != "" {
			return writeErrorResult(out, "--output not supported.")
		}
		if opts.cacheTo != "" {
			return writeErrorResult(out, "--cache-to not supported.")
		}
	}

	var imageNameResult []string

	if cfg.IsDockerfileConfig() {
		imageNameResult, err = run.buildDockerfile(ctx, cfg, loadResult, useBuildx)
	} else if cfg.IsComposeConfig() {
		imageNameResult, err = run.buildCompose(ctx, cfg, useBuildx)
	} else {
		// Image-based config
		imageNameResult, err = run.buildImage(ctx, cfg, loadResult, useBuildx)
	}

	if err != nil {
		if isUserFacingBuildError(err) {
			return writeErrorResult(out, err.Error())
		}
		return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
			Description: fmt.Sprintf("An error occurred building the container: %v", err),
		}))
	}

	return writeSuccessJSON(out, map[string]interface{}{
		"outcome":   "success",
		"imageName": imageNameResult,
	})
}

func (r *buildRunner) buildDockerfile(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	// Read Dockerfile
	dockerfilePath := cfg.Dockerfile()
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
		stageName, modifiedContent = docker.EnsureFinalStageName(string(content), "dev_container_auto_added_stage_label")
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
	if cfg.BuildContext() != "" {
		contextPath = filepath.Join(configDir, cfg.BuildContext())
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

	cacheFrom := cacheFromForDockerfileBuild(opts.cacheFrom, cfg)
	// Bridge the CLI credential chain to the build subprocess (private base pull /
	// --push / --cache-to) via a temporary DOCKER_CONFIG.
	authEnv, authCleanup := bridgeBuildAuth(logger, baseImage, imageNames, cacheFrom, opts.cacheTo)
	defer authCleanup()

	// Build secrets (Go extension; TS build has no --secrets-file). Passed to
	// BuildKit as `--secret id=KEY,env=KEY`, so a Dockerfile can `RUN
	// --mount=type=secret,id=KEY` without baking the value into a layer. Requires
	// buildx; ignored (with a warning) otherwise.
	var buildSecrets []string
	if opts.secretsFile != "" {
		secrets, secErr := readSecretsFile(opts.secretsFile)
		if secErr != nil {
			return nil, fmt.Errorf("read secrets file: %w", secErr)
		}
		if useBuildx {
			buildSecrets = secrets
		} else if len(secrets) > 0 {
			logger.Write("--secrets-file requires BuildKit (buildx); secrets ignored for the legacy builder", log.LevelWarning)
		}
	}

	buildOpts := docker.BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: contextPath,
		Tags:        imageNames,
		Target:      stageName,
		BuildArgs:   buildArgs,
		CacheFrom:   cacheFrom,
		Labels:      allLabels,
		NoCache:     opts.noCache,
		ExtraArgs:   buildOptionsFromConfig(cfg),
		UseBuildx:   useBuildx,
		Platform:    opts.platform,
		Push:        basePush,
		Output:      baseOutput,
		CacheTo:     opts.cacheTo,
		Env:         authEnv,
		Secrets:     buildSecrets,
	}

	result, err := dockerClient.Build(ctx, buildOpts)
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
			OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
			NoCache:                     opts.noCache,
			CacheFrom:                   opts.cacheFrom,
			CacheTo:                     opts.cacheTo,
			Labels:                      opts.labels,
			Platform:                    opts.platform,
			Push:                        opts.push,
			Output:                      opts.output,
			ContainerEnv:                cfg.ContainerEnv,
			SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
			Lockfile:                    opts.experimentalLockfile,
			FrozenLockfile:              opts.experimentalFrozenLockfile,
			NoLockfile:                  opts.noLockfile,
			ConfigPath:                  cfg.ConfigFilePath,
			LockfileExcludeIDs:          opts.lockfileExcludeIDs,
			SkipPersistCustoms:          opts.skipPersistCustoms,
			FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
			AdditionalMetadata:          []imagemeta.Entry{configToMetadataEntry(cfg)},
		})
	}

	return imageNames, nil
}

func (r *buildRunner) buildImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return nil, fmt.Errorf("no image specified in devcontainer.json")
	}

	// Pre-validate features before pulling the image, so local feature errors
	// surface immediately instead of after an expensive pull.
	if len(cfg.Features) > 0 {
		for id := range cfg.Features {
			srcType := features.ClassifyID(id)
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
			OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
			NoCache:                     opts.noCache,
			CacheFrom:                   opts.cacheFrom,
			CacheTo:                     opts.cacheTo,
			Labels:                      opts.labels,
			Platform:                    opts.platform,
			Push:                        opts.push,
			Output:                      opts.output,
			ContainerEnv:                cfg.ContainerEnv,
			SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
			Lockfile:                    opts.experimentalLockfile,
			FrozenLockfile:              opts.experimentalFrozenLockfile,
			NoLockfile:                  opts.noLockfile,
			ConfigPath:                  cfg.ConfigFilePath,
			LockfileExcludeIDs:          opts.lockfileExcludeIDs,
			SkipPersistCustoms:          opts.skipPersistCustoms,
			FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
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
				OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
				NoCache:                     opts.noCache,
				CacheFrom:                   opts.cacheFrom,
				CacheTo:                     opts.cacheTo,
				Labels:                      opts.labels,
				Platform:                    opts.platform,
				Push:                        opts.push,
				Output:                      opts.output,
				ContainerEnv:                cfg.ContainerEnv,
				SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
				Lockfile:                    opts.experimentalLockfile,
				FrozenLockfile:              opts.experimentalFrozenLockfile,
				NoLockfile:                  opts.noLockfile,
				ConfigPath:                  cfg.ConfigFilePath,
				LockfileExcludeIDs:          opts.lockfileExcludeIDs,
				SkipPersistCustoms:          opts.skipPersistCustoms,
				FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
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

		// Bridge auth for the push target / registry cache (base is local: cfg.Image
		// was already pulled above via the engine).
		authEnv, authCleanup := bridgeBuildAuth(logger, "", featureImageNames, opts.cacheFrom, opts.cacheTo)
		defer authCleanup()

		result, buildErr := dockerClient.Build(ctx, docker.BuildOptions{
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
			Env:         authEnv,
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

		result, buildErr := dockerClient.Build(ctx, docker.BuildOptions{
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
				if tagErr := dockerClient.Tag(ctx, cfg.Image, name); tagErr != nil {
					return nil, fmt.Errorf("build and tag image %q: %w", name, tagErr)
				}
			}
		}
		return imageNames, nil
	}

	// No image names, no platform/push — return base image as-is

	return imageNames, nil
}

// cacheFromForDockerfileBuild combines the --cache-from flag values with the
// devcontainer.json build.cacheFrom, in that order — matching TS singleContainer,
// which pushes the additional (flag) cache-froms first and then config.build.cacheFrom
// (string or array). It applies only to the build of the user's Dockerfile; the
// feature layers take the flag values alone (matching TS containerFeatures, which
// uses additionalCacheFroms only).
func cacheFromForDockerfileBuild(flagCacheFrom []string, cfg *config.DevContainer) []string {
	if cfg == nil || cfg.Build == nil || len(cfg.Build.CacheFrom) == 0 {
		return flagCacheFrom
	}
	combined := make([]string, 0, len(flagCacheFrom)+len(cfg.Build.CacheFrom))
	combined = append(combined, flagCacheFrom...)
	combined = append(combined, cfg.Build.CacheFrom...)
	return combined
}

func buildArgsFromConfig(cfg *config.DevContainer) map[string]string {
	if cfg.Build == nil || cfg.Build.Args == nil {
		return map[string]string{}
	}
	return cfg.Build.Args
}

func dockerfileBuildMetadataEntries(cfg *config.DevContainer, baseLabels map[string]string, logger log.Logger) []imagemeta.Entry {
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

func buildOptionsFromConfig(cfg *config.DevContainer) []string {
	if cfg.Build == nil || len(cfg.Build.Options) == 0 {
		return nil
	}
	return cfg.Build.Options
}

func writeSuccessJSON(out Output, data map[string]interface{}) error {
	b, _ := json.Marshal(data)
	fmt.Fprintln(out.Stdout(), string(b))
	return nil
}

func writeErrorResult(out Output, description string) error {
	return writeErrorJSON(out, coreerrors.ErrorOutput{
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
func writeValidationError(out Output, message string) error {
	fmt.Fprintln(out.Stderr(), message)
	return &coreerrors.ExitCodeError{Code: 1, Err: errors.New(message)}
}

func writeErrorJSON(out Output, errOutput coreerrors.ErrorOutput) error {
	b, _ := json.Marshal(errOutput)
	fmt.Fprintln(out.Stdout(), string(b))
	return &coreerrors.ExitCodeError{Code: 1, Err: errors.New(errOutput.Description)}
}

func isUserFacingBuildError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, legacyFeaturePrefix)
}

func (r *buildRunner) buildCompose(ctx context.Context, cfg *config.DevContainer, useBuildx bool) ([]string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Service == "" {
		return nil, fmt.Errorf("dockerComposeFile config requires 'service' property")
	}

	env := osEnvMap()
	composeFiles, err := config.DockerComposeFilePaths(cfg, env, filepath.Dir(cfg.ConfigFilePath))
	if err != nil {
		return nil, fmt.Errorf("resolve compose files: %w", err)
	}

	composeClient, err := docker.NewComposeClient(opts.dockerPath, opts.dockerComposePath, nil, logger)
	if err != nil {
		return nil, fmt.Errorf("compose client: %w", err)
	}

	composeConfig, err := composeClient.Config(ctx, composeFiles, "")
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
		buildErr := composeClient.Build(ctx, composeFiles, "", []string{"--project-name", projectName}, []string{cfg.Service}, opts.noCache)
		if buildErr != nil {
			return nil, fmt.Errorf("compose build: %w", buildErr)
		}
	}

	if len(cfg.Features) > 0 {
		if !hasBuild {
			if _, err := engine.InspectImage(ctx, baseImageName); err != nil {
				logger.Write(fmt.Sprintf("Pulling image %s...", baseImageName), log.LevelInfo)
				if pullErr := engine.PullImage(ctx, baseImageName); pullErr != nil {
					return nil, fmt.Errorf("pull image %q: %w", baseImageName, pullErr)
				}
			}
		}
		imageNames := opts.imageNames
		if len(imageNames) == 0 {
			imageNames = []string{baseImageName + "-features"}
		}
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, baseImageName, cfg.Features, useBuildx, imageNames, &FeatureBuildOptions{
			OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
			NoCache:                     opts.noCache,
			CacheFrom:                   opts.cacheFrom,
			CacheTo:                     opts.cacheTo,
			Labels:                      opts.labels,
			Platform:                    opts.platform,
			Push:                        opts.push,
			Output:                      opts.output,
			ContainerEnv:                cfg.ContainerEnv,
			SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
			Lockfile:                    opts.experimentalLockfile,
			FrozenLockfile:              opts.experimentalFrozenLockfile,
			NoLockfile:                  opts.noLockfile,
			ConfigPath:                  cfg.ConfigFilePath,
			LockfileExcludeIDs:          opts.lockfileExcludeIDs,
			SkipPersistCustoms:          opts.skipPersistCustoms,
			FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
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

		result, buildErr := dockerClient.Build(ctx, docker.BuildOptions{
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
