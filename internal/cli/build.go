package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/stringify"
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
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The help has always advertised a positional [path]; upstream #8 notes
			// it did nothing. Honor it as the workspace folder when --workspace-folder
			// is not given (the explicit flag wins on conflict), so `devcontainer
			// build ./project` works as users expect.
			if len(args) == 1 && opts.workspaceFolder == "" {
				opts.workspaceFolder = args[0]
			}
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
	// configLabel is the configuration's entry in the devcontainer.metadata
	// label: its properties as written (TS pick(config.raw, ...)).
	configLabel imagemeta.Entry
}

func runBuild(ctx context.Context, out Output, opts *buildOpts) error {
	// 0.88: --workspace-folder defaults to the current directory when not given.
	if opts.workspaceFolder == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}
	if err := validateEnums(
		enumFlag{"log-level", opts.logLevel, logLevels},
		enumFlag{"log-format", opts.logFormat, logFormats},
		enumFlag{"buildkit", opts.buildkit, []string{"auto", "never"}},
	); err != nil {
		return writeValidationError(out, err.Error())
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

	warnDeprecatedLockfileFlags(out.Stderr(), opts.experimentalLockfile, opts.experimentalFrozenLockfile)

	// Non-blocking hint (interactive TTY only) if the host was never checked or a
	// previous `devcontainer check` found a failing configuration.
	warnUncheckedHost(out)

	opts.foldFrozenLockfile()

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
	// Route the OCI auth diagnostic lines at this command's logger.
	ociAuthPolicy(ctx).SetLogger(logger)

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

	// TS build reports only the message and description of this error.
	if derr := enforceDisallowedFeatures(ctx, cfg, opts.additionalFeatures, logger, nil); derr != nil {
		return writeErrorJSON(out, coreerrors.ErrorOutput{Outcome: "error", Message: derr.Error(), Description: derr.Description})
	}

	// Setup Docker clients
	dockerClient := newBuildDockerClient(opts.dockerPath, opts.logFormat, logger)

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	run := &buildRunner{log: logger, docker: dockerClient, engine: engine, opts: opts}
	if run.configLabel, err = configLabelEntry(loadResult.RawConfig, false); err != nil {
		return writeErrorResult(out, err.Error())
	}

	// Detect BuildKit
	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit(ctx)
		useBuildx = bk.Available
	}

	if msg := buildFlagConflict(opts, useBuildx, cfg.IsComposeConfig()); msg != "" {
		return writeErrorResult(out, msg)
	}

	var imageNameResult []string

	if cfg.IsDockerfileConfig() {
		imageNameResult, err = run.buildDockerfile(ctx, cfg, loadResult, useBuildx)
	} else if cfg.IsComposeConfig() {
		imageNameResult, err = run.buildCompose(ctx, cfg, loadResult, useBuildx)
	} else {
		// Image-based config
		imageNameResult, err = run.buildImage(ctx, cfg, loadResult, useBuildx)
	}

	if err != nil {
		return writeErrorJSON(out, commandErrorOutput(err, "An error occurred building the container"))
	}

	return writeSuccessJSON(out, map[string]any{
		"outcome":            "success",
		"imageName":          buildResultImageName(cfg.IsComposeConfig(), opts.imageNames, imageNameResult),
		"ociAuthDiagnostics": ociAuthDiagnostics(ctx),
	}, buildResultKeys...)
}

// buildResultImageName is the result's imageName: the image names built, except
// that TS reports the one image a Compose build made without --image-name as a
// string (doBuild: imageNameResult = originalImageName).
func buildResultImageName(compose bool, argImageNames, built []string) any {
	if compose && len(argImageNames) == 0 {
		return built[0]
	}
	return built
}

// warnDeprecatedLockfileFlags writes the warnings TS up and build write
// straight to stderr for the deprecated lockfile flags
// (devContainersSpecCLI.ts warnDeprecatedLockfileFlags): the lockfile is
// written by default, so --experimental-lockfile does nothing more.
func warnDeprecatedLockfileFlags(w io.Writer, experimentalLockfile, experimentalFrozenLockfile bool) {
	if experimentalLockfile {
		fmt.Fprint(w, "Warning: --experimental-lockfile is deprecated. Lockfiles are now enabled by default.\n")
	}
	if experimentalFrozenLockfile {
		fmt.Fprint(w, "Warning: --experimental-frozen-lockfile is deprecated. Use --frozen-lockfile instead.\n")
	}
}

// foldFrozenLockfile folds the deprecated --experimental-frozen-lockfile into
// --frozen-lockfile, as TS does (effectiveFrozenLockfile = frozenLockfile ||
// experimentalFrozenLockfile); the Feature build reads the folded value.
func (o *buildOpts) foldFrozenLockfile() {
	o.experimentalFrozenLockfile = o.experimentalFrozenLockfile || o.frozenLockfile
}

// newBuildDockerClient is the Docker CLI client a build runs `docker build`
// with. It streams the build's progress live to stderr in text mode only: under
// --log-format json a raw byte stream would corrupt the structured events.
func newBuildDockerClient(dockerPath, logFormat string, logger log.Logger) *docker.Client {
	c := docker.NewClient(dockerPath, nil, logger)
	c.ProgressWriter = progressWriter(logFormat, os.Stderr)
	return c
}

// buildFlagConflict is the error a build reports for flags its builder or
// config cannot honour, or "" when there is none: --platform and --push need
// BuildKit, --push cannot go with --output, and a compose config takes none of
// --platform, --push, --output or --cache-to.
func buildFlagConflict(opts *buildOpts, useBuildx, compose bool) string {
	switch {
	case (opts.platform != "" || opts.push) && !useBuildx:
		return "--platform or --push require BuildKit enabled."
	case opts.output != "" && opts.push:
		return "--push true cannot be used with --output."
	case !compose:
		return ""
	case opts.platform != "" || opts.push:
		return "--platform or --push not supported."
	case opts.output != "":
		return "--output not supported."
	case opts.cacheTo != "":
		return "--cache-to not supported."
	}
	return ""
}

// dockerfileBuildPrep holds the resolved Dockerfile inputs shared by the `up`
// and `build` Dockerfile flows.
type dockerfileBuildPrep struct {
	Path      string             // resolved Dockerfile path (a temp .devcontainer.build file when a stage name had to be injected)
	StageName string             // build target: the config target, or the auto-added final stage name
	Parsed    *docker.Dockerfile // the parsed original Dockerfile
	cleanup   func()
}

// Cleanup removes any temporary Dockerfile written by prepareDockerfileBuild.
func (p *dockerfileBuildPrep) Cleanup() { p.cleanup() }

// copyDockerignore copies a Dockerfile's sibling `<name>.dockerignore` next to a
// renamed/generated build Dockerfile, so BuildKit's per-Dockerfile ignore lookup
// (`<dockerfile>.dockerignore`) still finds the user's rules instead of leaking
// excluded files into the build context (upstream #969). Best-effort: absent
// ignore file → no-op.
func copyDockerignore(origDockerfile, newDockerfile string) {
	data, err := os.ReadFile(origDockerfile + ".dockerignore")
	if err != nil {
		return
	}
	_ = os.WriteFile(newDockerfile+".dockerignore", data, 0644)
}

// configRelativePath is TS getConfigFilePath (configuration.ts): a path the
// configuration names, resolved against the configuration's folder, so an
// absolute one stays as it is.
func configRelativePath(cfg *config.DevContainer, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(filepath.Dir(cfg.ConfigFilePath), p)
}

// dockerfileConfigPath is TS getDockerfilePath: the configuration's Dockerfile.
func dockerfileConfigPath(cfg *config.DevContainer) string {
	return configRelativePath(cfg, cfg.Dockerfile())
}

// dockerContextPath is TS getDockerContextPath (spec-node/utils.ts): the
// configured build context, else the Dockerfile's folder.
func dockerContextPath(cfg *config.DevContainer) string {
	if c := cfg.BuildContext(); c != "" {
		return configRelativePath(cfg, c)
	}
	return filepath.Dir(dockerfileConfigPath(cfg))
}

// prepareDockerfileBuild resolves cfg's Dockerfile relative to the config dir,
// reads it, ensures the final stage has a name (writing a temp Dockerfile when a
// name must be injected), and parses it. The caller must defer prep.Cleanup().
// It deliberately stops before base-image resolution and metadata, which differ
// between the up and build flows.
func prepareDockerfileBuild(cfg *config.DevContainer) (*dockerfileBuildPrep, error) {
	dockerfilePath := cfg.Dockerfile()
	if dockerfilePath == "" {
		return nil, fmt.Errorf("no Dockerfile specified")
	}
	dockerfilePath = dockerfileConfigPath(cfg)

	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile: %w", err)
	}

	prep := &dockerfileBuildPrep{
		Parsed:  docker.ExtractDockerfile(string(content)),
		cleanup: func() {},
	}

	if cfg.Build != nil && cfg.Build.Target != "" {
		prep.StageName = cfg.Build.Target
	} else {
		stageName, modified, err := docker.EnsureFinalStageName(string(content), "dev_container_auto_added_stage_label")
		if err != nil {
			return nil, err
		}
		prep.StageName = stageName
		if modified != "" {
			tmpDockerfile := dockerfilePath + ".devcontainer.build"
			// mutate-exempt: only the docker build this process starts reads the file, as its owner, and Cleanup removes it
			if err := os.WriteFile(tmpDockerfile, []byte(modified), 0644); err != nil {
				return nil, fmt.Errorf("write build dockerfile: %w", err)
			}
			// Mirror the user's sibling <Dockerfile>.dockerignore next to the
			// renamed file so BuildKit still applies it (upstream #969).
			copyDockerignore(dockerfilePath, tmpDockerfile)
			prep.cleanup = func() {
				os.Remove(tmpDockerfile)
				os.Remove(tmpDockerfile + ".dockerignore")
			}
			dockerfilePath = tmpDockerfile
		}
	}

	prep.Path = dockerfilePath
	return prep, nil
}

// buildDockerfile builds a Dockerfile config's image, then its Features on top.
//
// mutate-lane: TestParityMatrix (build.dockerfile-multi-image-success,
// build.subfolder-config-success, build.apply-build-options-success,
// build.output-oci-success, build.cache-to-local-success,
// build.platform-supported-dockerfile-success), also run by
// TestE2E_BuildUpExec_Dockerfile and TestE2E_SiblingDockerignore.
func (r *buildRunner) buildDockerfile(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts

	prep, err := prepareDockerfileBuild(cfg)
	if err != nil {
		return nil, err
	}
	defer prep.Cleanup()
	dockerfilePath := prep.Path
	stageName := prep.StageName

	// The base image is the FROM of the config's target, else of the last stage
	// (TS getImageBuildInfoFromDockerfile with config.build.target), not of the
	// stage name added to the Dockerfile.
	findTarget := ""
	if cfg.Build != nil {
		findTarget = cfg.Build.Target
	}
	baseImage := docker.FindBaseImage(prep.Parsed, buildArgsFromConfig(cfg), findTarget, buildxPlatformArgs(opts.platform))
	logger.Write(fmt.Sprintf("Base image: %s", baseImage), log.LevelInfo)

	// Generate base-image metadata. Ensure the base image is available locally so
	// metadata from the published image label is preserved.
	baseLabels := engine.ImageLabelsEnsuringPresent(ctx, baseImage)
	baseMetadata := imagemeta.ReadMetadataFromLabels(baseLabels, logger)
	stageMetadata := imagemeta.ReadMetadataFromLabels(prep.Parsed.StageLabels(stageName), logger)

	// Determine image names (match TS getFolderImageName: vsc-{basename}-{hash})
	imageNames := imageNamesOr(opts.imageNames, folderImageName(resolvePath(opts.workspaceFolder)))

	contextPath := dockerContextPath(cfg)

	// Build args from config
	buildArgs := buildArgsFromConfig(cfg)

	// Add metadata label
	allLabels := append(slices.Clone(opts.labels), imageLabelArgs(dockerfileImageMetadata(cfg, baseMetadata, stageMetadata, r.configLabel))...)

	baseOutput, basePush := dockerfileBaseExport(opts, cfg)

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
		buildSecrets = buildxSecrets(secrets, useBuildx, logger)
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

	// When streaming, the build output already reached the terminal live; only
	// emit the buffered copy when it did not (e.g. --log-format json).
	if dockerClient.ProgressWriter == nil {
		logger.Write(string(result.Stderr), log.LevelInfo)
	}

	// If config has features, extend the built image with features
	if len(cfg.Features) > 0 {
		baseImageName := imageNames[0]
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, baseImageName, cfg.Features, useBuildx, imageNames, r.featureBuildOptions(cfg, &loadResult.HostSub))
	}

	return imageNames, nil
}

// buildImage produces an image config's image: the image itself, or a build on
// top of it for Features, --platform/--push/--output or --image-name.
//
// mutate-lane: TestParityMatrix (build.image-success,
// build.image-multi-image-success, build.image-metadata-label-success,
// build.image-extra-cache-from-success, build.no-cache-features-success,
// build.cache-to-local-image-success,
// build.platform-supported-image-with-features-success), also run by
// TestE2E_BuildUpExec_Image.
func (r *buildRunner) buildImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return nil, fmt.Errorf("no image specified in devcontainer.json")
	}

	// Pre-validate features before pulling the image, so local feature errors
	// surface immediately instead of after an expensive pull.
	if err := checkLegacyFeatures(cfg.Features); err != nil {
		return nil, err
	}

	logger.Write(fmt.Sprintf("Using image: %s", cfg.Image), log.LevelInfo)

	folderImage := folderImageName(resolvePath(opts.workspaceFolder))
	imageNames := imageNamesOr(opts.imageNames, folderImage)
	featureImageNames := imageNamesOr(opts.imageNames, folderImage+"-features")

	// Ensure image exists locally
	info, err := engine.InspectImage(ctx, cfg.Image)
	if err != nil {
		logger.Write(fmt.Sprintf("Pulling image %s...", cfg.Image), log.LevelInfo)
		if pullErr := engine.PullImage(ctx, cfg.Image); pullErr != nil {
			return nil, fmt.Errorf("image %q not found locally and pull failed: %w", cfg.Image, pullErr)
		}
		if info, err = engine.InspectImage(ctx, cfg.Image); err != nil {
			return nil, fmt.Errorf("inspect image %q: %w", cfg.Image, err)
		}
	}
	// The label of an image build without Features (TS getImageBuildOptions):
	// the image's metadata, then the configuration's entry as written.
	var imageMetadata []imagemeta.Entry
	if info.Config != nil {
		imageMetadata = imagemeta.ReadMetadataFromLabels(info.Config.Labels, logger)
	}
	imageMetadata = append(imageMetadata, r.configLabel)

	switch imageBuildFor(cfg, opts) {
	case imageWithFeatures:
		return extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, featureImageNames, r.featureBuildOptions(cfg, &loadResult.HostSub))

	case imageExported:
		// --platform, --push, or --output still need a build: a minimal FROM
		// Dockerfile carrying the metadata (TS getImageBuildOptions).
		allLabels := append(slices.Clone(opts.labels), imageLabelArgs(imageMetadata)...)

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
		if dockerClient.ProgressWriter == nil {
			logger.Write(string(result.Stderr), log.LevelInfo)
		}
		return featureImageNames, nil

	case imageRelabeled:
		// No platform/push — use temp Dockerfile when imageNames require labeling
		allLabels := append(slices.Clone(opts.labels), imageLabelArgs(imageMetadata)...)

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

// imageBuild is what `build` makes of an image config.
type imageBuild int

const (
	imageAsIs         imageBuild = iota // the image itself, under the folder's image name
	imageWithFeatures                   // a Feature build on top of the image
	imageExported                       // a FROM build for --platform, --push or --output
	imageRelabeled                      // a FROM build stamping the metadata label under --image-name
)

// imageBuildFor is what `build` makes of the image config cfg: Features win,
// then --platform/--push/--output, then --image-name.
func imageBuildFor(cfg *config.DevContainer, opts *buildOpts) imageBuild {
	switch {
	case len(cfg.Features) > 0:
		return imageWithFeatures
	case opts.platform != "" || opts.push || opts.output != "":
		return imageExported
	case len(opts.imageNames) > 0:
		return imageRelabeled
	}
	return imageAsIs
}

// checkLegacyFeatures fails on a bare-id (legacy) Feature the CLI no longer
// maps to a published one; a tag (go:1) is not part of the name looked up.
func checkLegacyFeatures(feats map[string]any) error {
	for id := range feats {
		if features.ClassifyID(id) != features.SourceLegacyShorthand {
			continue
		}
		name := id
		if i := strings.LastIndexByte(id, ':'); i != -1 {
			name = id[:i]
		}
		if !features.IsKnownLegacyFeature(name) {
			return &coreerrors.ContainerError{Description: fmt.Sprintf(msgLegacyFeature, id)}
		}
	}
	return nil
}

// imageNamesOr is the --image-name values, or fallback when none was given.
func imageNamesOr(names []string, fallback string) []string {
	if len(names) == 0 {
		return []string{fallback}
	}
	return names
}

// dockerfileImageMetadata is the metadata a Dockerfile build stamps on its
// image: the base image's, then a `LABEL devcontainer.metadata` the user's
// Dockerfile declares (#1225; the CLI's own label would otherwise clobber it),
// then the config's entry as written (configLabel). With Features the Feature
// build appends that entry instead.
func dockerfileImageMetadata(cfg *config.DevContainer, base, stage []imagemeta.Entry, configLabel imagemeta.Entry) []imagemeta.Entry {
	metadata := append(append([]imagemeta.Entry{}, base...), stage...)
	if len(cfg.Features) == 0 {
		metadata = append(metadata, configLabel)
	}
	return metadata
}

// dockerfileBaseExport is the --output and --push the Dockerfile build applies.
// With Features the base image is an intermediate build: it must land in the
// local image store so the Feature build can reference it as
// $_DEV_CONTAINERS_BASE_IMAGE, so --output/--push are left to the Feature build;
// applying them here would export the base without loading it.
func dockerfileBaseExport(opts *buildOpts, cfg *config.DevContainer) (output string, push bool) {
	if len(cfg.Features) > 0 {
		return "", false
	}
	return opts.output, opts.push
}

// buildxSecrets is the --secrets-file secrets passed to BuildKit. The legacy
// builder takes none, so without buildx they are dropped with a warning.
func buildxSecrets(secrets []string, useBuildx bool, logger log.Logger) []string {
	if useBuildx {
		return secrets
	}
	if len(secrets) > 0 {
		logger.Write("--secrets-file requires BuildKit (buildx); secrets ignored for the legacy builder", log.LevelWarning)
	}
	return nil
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
	combined := make([]string, len(flagCacheFrom))
	copy(combined, flagCacheFrom)
	combined = append(combined, cfg.Build.CacheFrom...)
	return combined
}

func buildArgsFromConfig(cfg *config.DevContainer) map[string]string {
	if cfg.Build == nil || cfg.Build.Args == nil {
		return map[string]string{}
	}
	return cfg.Build.Args
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

// writeSuccessJSON writes a command's result with its keys in order (see
// orderedObject).
func writeSuccessJSON(out Output, data map[string]any, order ...string) error {
	b, err := stringify.JSON(orderedObject{data, order}, "")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
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
	b, _ := stringify.JSON(errOutput, "")
	fmt.Fprintln(out.Stdout(), string(b))
	return &coreerrors.ExitCodeError{Code: 1, Err: errors.New(errOutput.Description)}
}

// commandErrorOutput is the error result of up and build: a ContainerError (TS
// throws one with its own description, which the commands report as it is)
// as it is; an error carrying the reference's own message as TS reports any
// error it wraps, that message under "<description>."; any other error
// described as "<description>: <error>".
func commandErrorOutput(err error, description string) coreerrors.ErrorOutput {
	if ce, ok := coreerrors.AsContainerError(err); ok {
		return coreerrors.ToErrorOutput(ce)
	}
	// TS wraps a plain Error it throws as its originalError, so the message is
	// that error's own text (a frozen lockfile's "Lockfile does not match.", a
	// lockfile's JSON.parse error).
	if re, ok := errors.AsType[coreerrors.ReferenceError](err); ok {
		return coreerrors.ErrorOutput{Outcome: "error", Message: re.Error(), Description: description + "."}
	}
	return coreerrors.ToErrorOutput(&coreerrors.ContainerError{Description: fmt.Sprintf("%s: %v", description, err)})
}

// buildCompose is TS build for a Docker Compose configuration: the service's
// image is built by `docker compose build` through the build override TS
// buildAndExtendDockerCompose writes, with the metadata label on the image
// (with or without Features, an image-only service included), so the image
// carries compose's project and service labels; then it is tagged with each
// --image-name.
//
// mutate-lane: TestParityMatrix (build.compose-dockerfile-multi-image-success,
// build.compose-image-without-features-minimal-success,
// build.compose-image-without-features-minimal-metadata-label-success,
// build.compose-image-with-features-success,
// build.compose-image-extra-cache-from-success), also run by
// TestE2E_ComposeBuildLabel and TestDifferentialBuild.
func (r *buildRunner) buildCompose(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, useBuildx bool) ([]string, error) {
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
	composeClient.ProgressWriter = progressWriter(opts.logFormat, os.Stderr)

	composeConfig, err := composeClient.Config(ctx, composeFiles, "")
	if err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	projectName := resolveComposeProjectName(ctx, cfg, env, composeFiles, composeClient)
	svc, ok := composeService(composeConfig, cfg.Service)
	if !ok {
		return nil, fmt.Errorf("Service '%s' configured in devcontainer.json not found in Docker Compose configuration.", cfg.Service)
	}
	versionPrefix, err := readComposeVersionPrefix(composeFiles)
	if err != nil {
		return nil, err
	}
	if opts.secretsFile != "" {
		logger.Write("--secrets-file does not apply to a Docker Compose configuration: `docker compose build` takes no build secrets.", log.LevelWarning)
	}

	run := &upRunner{
		log:    logger,
		docker: dockerClient,
		engine: engine,
		opts: &upOpts{
			workspaceFolder:            opts.workspaceFolder,
			cacheFrom:                  opts.cacheFrom,
			buildNoCache:               opts.noCache,
			skipFeatureAutoMapping:     opts.skipFeatureAutoMapping,
			experimentalFrozenLockfile: opts.experimentalFrozenLockfile,
			noLockfile:                 opts.noLockfile,
			lockfileExcludeIDs:         opts.lockfileExcludeIDs,
		},
		substitute:  hostMetadataSubstitution(loadResult.HostSub, nil),
		configLabel: r.configLabel,
	}
	ext, err := run.buildAndExtendCompose(ctx, composeStart{
		cfg:                cfg,
		loadResult:         loadResult,
		workspaceFolder:    resolvePath(opts.workspaceFolder),
		useBuildx:          useBuildx,
		projectName:        projectName,
		composeFiles:       composeFiles,
		composeConfig:      composeConfig,
		client:             composeClient,
		metadataOnImage:    true,
		buildServices:      []string{cfg.Service},
		skipPersistCustoms: opts.skipPersistCustoms,
	}, cacheFolder(), versionPrefix, false)
	if err != nil {
		return nil, err
	}

	imageName := ext.overrideImageName
	if imageName == "" {
		imageName, _ = svc["image"].(string)
	}
	if imageName == "" {
		imageName = composeClient.DefaultImageName(projectName, cfg.Service)
	}
	tags := imageNamesOr(opts.imageNames, imageName)
	if len(opts.labels) > 0 {
		// compose build takes no --label: a FROM build applies them (upstream
		// #930), under the --image-name values or the image's own name.
		return tags, labelComposeImage(ctx, dockerClient, imageName, tags, opts.labels, useBuildx)
	}
	for _, name := range opts.imageNames {
		if err := dockerClient.Tag(ctx, imageName, name); err != nil {
			return nil, fmt.Errorf("tag image %q: %w", name, err)
		}
	}
	return tags, nil
}

// labelComposeImage builds `FROM imageName` with labels, tagged tags.
//
// mutate-lane: TestE2E_ComposeBuildLabel - builds a compose image with --label
// and reads the label back.
func labelComposeImage(ctx context.Context, dockerClient *docker.Client, imageName string, tags, labels []string, useBuildx bool) error {
	tmpDir, err := os.MkdirTemp("", "devcontainer-compose-image-build-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	dfPath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dfPath, []byte(fmt.Sprintf("FROM %s\n", imageName)), 0o644); err != nil {
		return fmt.Errorf("write temp Dockerfile: %w", err)
	}
	result, err := dockerClient.Build(ctx, docker.BuildOptions{
		Dockerfile:  dfPath,
		ContextPath: tmpDir,
		Tags:        tags,
		Labels:      labels,
		UseBuildx:   useBuildx,
	})
	if err != nil {
		return fmt.Errorf("build image: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(msgDockerBuildFailed, result.ExitCode, string(result.Stderr))
	}
	return nil
}

// featureBuildOptions are the Feature install options every build path passes:
// the build flags and the config's Feature settings. hostSub resolves the
// Features' ${localEnv:…} references (nil leaves them as written). The
// configuration's entry follows the Features' in the image's metadata label.
func (r *buildRunner) featureBuildOptions(cfg *config.DevContainer, hostSub *config.HostSubContext) *FeatureBuildOptions {
	opts := r.opts
	return &FeatureBuildOptions{
		Secrets:                     buildSecretsFromFile(opts.secretsFile),
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
		FrozenLockfile:              opts.experimentalFrozenLockfile,
		NoLockfile:                  opts.noLockfile,
		ConfigPath:                  cfg.ConfigFilePath,
		LockfileExcludeIDs:          opts.lockfileExcludeIDs,
		HostSub:                     hostSub,
		SkipPersistCustoms:          opts.skipPersistCustoms,
		FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
		WorkspaceFolder:             opts.workspaceFolder,
		AdditionalMetadata:          []imagemeta.Entry{configToMetadataEntry(cfg)},
		LabelMetadata:               []imagemeta.Entry{r.configLabel},
	}
}
