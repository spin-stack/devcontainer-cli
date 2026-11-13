package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	dockermount "github.com/moby/moby/api/types/mount"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

type upOpts struct {
	workspaceFolder             string
	configPath                  string
	overrideConfig              string
	lockfileExcludeIDs          map[string]bool
	dockerPath                  string
	dockerComposePath           string
	logLevel                    string
	logFormat                   string
	logFile                     string
	terminalLogFile             string
	mountWorkspaceGitRoot       bool
	removeExisting              bool
	buildNoCache                bool
	skipPostCreate              bool
	skipNonBlocking             bool
	skipPostAttach              bool
	prebuild                    bool
	cacheImage                  string
	buildkit                    string
	idLabels                    []string
	mounts                      []string
	remoteEnvs                  []string
	includeConfig               bool
	includeMergedConfig         bool
	userDataFolder              string
	expectExistingContainer     bool
	additionalFeatures          string
	cacheFrom                   []string
	cacheTo                     string
	dotfilesRepo                string
	dotfilesCommand             string
	dotfilesTarget              string
	omitSyntaxDirective         bool
	omitConfigRemoteEnvFromMeta bool
	terminalColumns             givenInt
	terminalRows                givenInt
	secretsFile                 string
	secrets                     fileSecrets
	caBundle                    string
	experimentalLockfile        bool
	experimentalFrozenLockfile  bool
	noLockfile                  bool
	frozenLockfile              bool
	mountGitWorktreeCommonDir   bool
	workspaceMountConsistency   string
	updateRemoteUserUIDDefault  string
	gpuAvailability             string
	platform                    string
	defaultUserEnvProbe         string
	containerSessionDataFolder  string
	composeProjectName          string
}

type upRunner struct {
	out          Output
	log          log.Logger
	docker       *docker.Client
	engine       *docker.EngineClient
	opts         *upOpts
	idLabels     []string
	substitute   metadataSubstitution
	configLabel  imagemeta.Entry
	lockfileRead bool
}

func newUpCmd() *cobra.Command {
	var opts upOpts

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create and run dev container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(cmd.Context(), outputFor(cmd), &opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.overrideConfig, "override-config", "", "Override devcontainer.json path.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")
	f.BoolVar(&opts.mountWorkspaceGitRoot, "mount-workspace-git-root", true, "Mount workspace using Git root.")
	f.BoolVar(&opts.removeExisting, "remove-existing-container", false, "Remove existing container.")
	f.BoolVar(&opts.buildNoCache, "build-no-cache", false, "Build with --no-cache.")
	f.BoolVar(&opts.skipPostCreate, "skip-post-create", false, "Skip post-create commands.")
	f.BoolVar(&opts.skipNonBlocking, "skip-non-blocking-commands", false, "Stop after waitFor command.")
	f.BoolVar(&opts.skipPostAttach, "skip-post-attach", false, "Skip postAttachCommand.")
	f.BoolVar(&opts.prebuild, "prebuild", false, "Stop after updateContentCommand.")
	f.StringVar(&opts.cacheImage, "cache-image", "", "Start from this prebuilt image, skipping build and feature install (not for Compose configs).")
	f.StringVar(&opts.buildkit, "buildkit", "auto", "BuildKit mode.")
	f.StringArrayVar(&opts.idLabels, "id-label", nil, "Id label(s).")
	f.StringArrayVar(&opts.mounts, "mount", nil, "Additional mount(s).")
	f.StringArrayVar(&opts.remoteEnvs, "remote-env", nil, "Remote env vars.")
	f.BoolVar(&opts.includeConfig, "include-configuration", false, "Include configuration in result.")
	f.BoolVar(&opts.includeMergedConfig, "include-merged-configuration", false, "Include merged config.")

	f.StringVar(&opts.dockerComposePath, "docker-compose-path", "", "")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.StringVar(&opts.workspaceMountConsistency, "workspace-mount-consistency", "cached", "Mount consistency.")
	f.StringVar(&opts.gpuAvailability, "gpu-availability", "detect", "GPU availability (all|detect|none).")
	f.StringVar(&opts.platform, "platform", "", "Target platform (e.g. linux/amd64). Used to resolve, pull and build the image; overrides a --platform in runArgs.")
	f.StringVar(&opts.defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "Env probe type.")
	f.StringVar(&opts.updateRemoteUserUIDDefault, "update-remote-user-uid-default", "on", "UID update default.")
	f.BoolVar(&opts.expectExistingContainer, "expect-existing-container", false, "Fail if no existing container is found.")
	f.StringVar(&opts.userDataFolder, "user-data-folder", "", "Host path for persisted state.")
	f.StringArrayVar(&opts.cacheFrom, "cache-from", nil, "Cache from images.")
	f.StringVar(&opts.cacheTo, "cache-to", "", "Cache to destination.")
	f.StringVar(&opts.additionalFeatures, "additional-features", "", "Additional features JSON.")
	f.Bool("skip-feature-auto-mapping", false, "")
	f.StringVar(&opts.dotfilesRepo, "dotfiles-repository", "", "Dotfiles repo.")
	f.StringVar(&opts.dotfilesCommand, "dotfiles-install-command", "", "Dotfiles install command.")
	f.StringVar(&opts.dotfilesTarget, "dotfiles-target-path", "~/dotfiles", "Dotfiles target.")
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "Session data folder.")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "Secrets file path.")
	f.StringVar(&opts.caBundle, "ca-bundle", "", caBundleFlagUsage)
	f.BoolVar(&opts.experimentalLockfile, "experimental-lockfile", false, "")
	f.BoolVar(&opts.experimentalFrozenLockfile, "experimental-frozen-lockfile", false, "")
	f.BoolVar(&opts.noLockfile, "no-lockfile", false, "Disable lockfile generation and verification.")
	f.BoolVar(&opts.frozenLockfile, "frozen-lockfile", false, "Ensure lockfile exists and remains unchanged; fail otherwise.")
	f.BoolVar(&opts.mountGitWorktreeCommonDir, "mount-git-worktree-common-dir", false, "Mount the Git worktree common dir for Git operations to work in the container (requires the worktree created with `git worktree add --relative-paths`).")
	f.BoolVar(&opts.omitSyntaxDirective, "omit-syntax-directive", false, "")
	f.BoolVar(&opts.omitConfigRemoteEnvFromMeta, "omit-config-remote-env-from-metadata", false, "")
	for _, h := range []string{
		"skip-feature-auto-mapping",
		"experimental-lockfile",
		"experimental-frozen-lockfile",
		"omit-syntax-directive",
		"omit-config-remote-env-from-metadata",
	} {
		_ = f.MarkHidden(h)
	}
	f.Var(&opts.terminalColumns, "terminal-columns", "")
	f.Var(&opts.terminalRows, "terminal-rows", "")
	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)

	return cmd
}

func validateUpOpts(opts *upOpts) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return err
	}
	if err := validateMounts(opts.mounts); err != nil {
		return err
	}
	if err := validateEnums(
		enumFlag{"log-level", opts.logLevel, logLevels},
		enumFlag{"log-format", opts.logFormat, logFormats},
		enumFlag{"buildkit", opts.buildkit, []string{"auto", "never"}},
		enumFlag{"workspace-mount-consistency", opts.workspaceMountConsistency, []string{"consistent", "cached", "delegated"}},
		enumFlag{"gpu-availability", opts.gpuAvailability, []string{"all", "detect", "none"}},
		enumFlag{"default-user-env-probe", opts.defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
		enumFlag{"update-remote-user-uid-default", opts.updateRemoteUserUIDDefault, []string{"on", "off", "never"}},
	); err != nil {
		return err
	}
	return validateTerminalImplications(opts.terminalColumns, opts.terminalRows)
}

func resolveUpPaths(opts *upOpts) (workspaceFolder, configPath, overridePath string) {
	if opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.overrideConfig == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}
	if opts.workspaceFolder != "" {
		workspaceFolder = resolvePath(opts.workspaceFolder)
	}
	if opts.configPath != "" {
		configPath = resolvePath(opts.configPath)
	}
	if opts.overrideConfig != "" {
		overridePath = resolvePath(opts.overrideConfig)
	}
	return
}

func upHasNothingToRead(workspaceFolder, configPath, overridePath string) bool {
	return workspaceFolder == "" && configPath == "" && overridePath == ""
}

// mutate-lane: TestParityMatrix - every `up` case of docs/parity/parity-matrix.yaml
// runs it, e.g. up.image-success, up.compose-with-name-success,
// up.id-label-only-existing-container-error, up.config-without-workspace-success,
// up.compose-dockerfile-with-features-reuse-stopped-container-with-persisted-overrides-success
// and up.image-post-create-remote-env-success; also TestE2E_UpReusesStoppedContainer
// (restarts a stopped single container), TestE2E_BuildUpExec_Image,
// TestE2E_RemoveExistingBeforeInitialize and TestE2E_CABundle.
func runUp(ctx context.Context, out Output, opts *upOpts) error {
	if err := validateUpOpts(opts); err != nil {
		return writeValidationError(out, err.Error())
	}
	var err error
	if opts.caBundle, err = resolveCABundle(opts.caBundle); err != nil {
		return writeValidationError(out, err.Error())
	}
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
	opts.experimentalFrozenLockfile = opts.experimentalFrozenLockfile || opts.frozenLockfile
	warnUncheckedHost(out)

	workspaceFolder, configPath, overridePath := resolveUpPaths(opts)

	logDst, closeLog, logErr := logWriter(opts.logFile, opts.terminalLogFile)
	if logErr != nil {
		return writeErrorResult(out, fmt.Sprintf("open log file: %v", logErr))
	}
	defer closeLog()

	if opts.secrets, err = loadSecretsFile(opts.secretsFile); err != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(err))
	}
	maskValues, err := opts.secrets.maskValues()
	if err != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{Description: "An error occurred setting up the container.", OriginalError: err}))
	}

	logger := log.New(log.Options{
		Version: cliVersion(),
		Level:   log.ParseLevel(opts.logLevel),
		Format:  opts.logFormat,
		Writer:  logDst,
		Secrets: maskValues,
	})
	ociAuthPolicy(ctx).SetLogger(logger)

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	run := &upRunner{out: out, log: logger, engine: engine, opts: opts}

	idLabels := opts.idLabels

	if upHasNothingToRead(workspaceFolder, configPath, overridePath) {
		return writeErrorResult(out, "No dev container config and no workspace found.")
	}

	loadResult, err := config.LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overridePath, config.MountOptions{
		MountWorkspaceGitRoot:     opts.mountWorkspaceGitRoot,
		MountGitWorktreeCommonDir: opts.mountGitWorktreeCommonDir,
	})
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	cfg := loadResult.Config

	if len(idLabels) == 0 {
		idLabels = []string{
			fmt.Sprintf("devcontainer.local_folder=%s", workspaceFolder),
		}
		if loadResult.Config.ConfigFilePath != "" {
			idLabels = append(idLabels, fmt.Sprintf("devcontainer.config_file=%s", loadResult.Config.ConfigFilePath))
		}
	}

	var mergeErr error
	opts.lockfileExcludeIDs, mergeErr = mergeAdditionalFeatures(cfg, opts.additionalFeatures)
	if mergeErr != nil {
		return writeErrorResult(out, mergeErr.Error())
	}

	idCtx := config.SubstitutionContext{
		HostSubContext: loadResult.HostSub,
		DevContainerID: config.ComputeDevContainerID(config.EnvMap(idLabels)),
	}
	resolver := config.NewVariableResolver()
	if err := resolver.BeforeContainerInto(idCtx, cfg); err != nil {
		return writeErrorResult(out, fmt.Sprintf("apply pre-container substitutions: %v", err))
	}
	if err := resolver.BeforeContainerInto(idCtx, loadResult.WorkspaceConfig); err != nil {
		return writeErrorResult(out, fmt.Sprintf("apply workspace pre-container substitutions: %v", err))
	}
	run.idLabels = idLabels
	run.substitute = hostMetadataSubstitution(loadResult.HostSub, idLabels)
	if run.configLabel, err = configLabelEntry(loadResult.RawConfig, loadResult.Config.ContainerEnvOrder, opts.omitConfigRemoteEnvFromMeta); err != nil {
		return writeErrorResult(out, err.Error())
	}

	existingIDs, _ := engine.ListContainers(ctx, true, idLabels)
	if len(existingIDs) > 0 && opts.removeExisting {
		if err := removeExistingContainer(ctx, engine, logger, cfg, opts.dockerPath, existingIDs[0]); err != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(err))
		}
		existingIDs = nil
	}

	if derr := enforceDisallowedFeatures(ctx, cfg, opts.additionalFeatures, logger, stopRunningContainer(engine, idLabels)); derr != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(derr))
	}

	if !cfg.InitializeCommand.IsEmpty() {
		if err := lifecycle.RunInitializeCommand(ctx, logger, &cfg.InitializeCommand, loadResult.HostSub); err != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: "The initializeCommand in the devcontainer.json failed.",
			}))
		}
	}

	dockerClient := docker.NewClient(opts.dockerPath, nil, logger)
	dockerClient.ProgressWriter = progressWriter(opts.logFormat, logDst)
	run.docker = dockerClient

	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit(ctx)
		useBuildx = bk.Available
	}

	var containerID string

	if len(existingIDs) > 0 && !cfg.IsComposeConfig() {
		containerID = existingIDs[0]
		logger.Write(fmt.Sprintf("Using existing container %s", containerID), log.LevelInfo)

		inspectResp, inspErr := engine.InspectContainer(ctx, containerID)
		if inspErr == nil && inspectResp.State != nil && !inspectResp.State.Running {
			logger.Write("Starting existing container...", log.LevelInfo)
			if startErr := engine.StartContainer(ctx, containerID); startErr != nil {
				return writeErrorResult(out, fmt.Sprintf("Failed to start container: %v", startErr))
			}
		}
	} else if len(existingIDs) == 0 && opts.expectExistingContainer && !cfg.IsComposeConfig() {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
			Description: "The expected container does not exist.",
		}))
	} else {
		// New container, or compose reuse. Compose always goes through
		// upFromCompose (even when an existing container was found) so that
		// `compose up` restarts the whole project — a direct StartContainer on
		// the app service fails when a dependency it shares a network namespace
		// with (e.g. db via `network_mode: service:db`) is stopped. upFromCompose
		// handles the existing-container and expect-existing-container cases.
		if opts.cacheImage != "" {
			if cfg.IsComposeConfig() {
				return writeValidationError(out, "--cache-image is not supported with a Docker Compose configuration.")
			}
			containerID, err = run.fromCacheImage(ctx, cfg, loadResult, workspaceFolder, idLabels)
		} else if cfg.IsDockerfileConfig() {
			containerID, err = run.fromDockerfile(ctx, cfg, loadResult, workspaceFolder, idLabels, useBuildx)
		} else if cfg.IsComposeConfig() {
			containerID, err = run.fromCompose(ctx, cfg, loadResult, workspaceFolder, idLabels, useBuildx)
		} else {
			containerID, err = run.fromImage(ctx, cfg, loadResult, workspaceFolder, idLabels, useBuildx)
		}
		if err != nil {
			return writeErrorJSON(out, commandErrorOutput(err, "An error occurred setting up the container"))
		}
	}

	inspectResp, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Failed to inspect container: %v", err))
	}

	imageLabels := func(ref string) map[string]string { return run.imageLabels(ctx, ref) }
	remoteUser := upRemoteUser(cfg.RemoteUser, inspectResp, imageLabels, logger)

	remoteWorkspaceFolder := loadResult.WorkspaceConfig.WorkspaceFolder

	if subErr := run.checkContainerSubstitution(inspectResp, cfg, loadResult.Raw); subErr != nil {
		ce, ok := coreerrors.AsContainerError(subErr)
		if !ok {
			ce = &coreerrors.ContainerError{Description: "An error occurred setting up the container.", OriginalError: subErr}
		}
		ce.ContainerID = containerID
		return writeErrorJSON(out, coreerrors.ToErrorOutput(ce))
	}

	if !opts.skipPostCreate {
		if lcErr := run.runLifecycleForUp(ctx, containerID, cfg, remoteWorkspaceFolder, remoteUser); lcErr != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(lcErr))
		}
	}

	result := map[string]any{
		"outcome":            "success",
		"containerId":        containerID,
		"remoteUser":         remoteUser,
		"ociAuthDiagnostics": ociAuthDiagnostics(ctx),
	}
	if remoteWorkspaceFolder != "" {
		result["remoteWorkspaceFolder"] = remoteWorkspaceFolder
	}
	if opts.composeProjectName != "" {
		result["composeProjectName"] = opts.composeProjectName
	}

	if err := addUpConfiguration(result, opts, upConfigFileFSPathRead(opts, run.lockfileRead), cfg, inspectResp, run.metadataOfContainer, logger); err != nil {
		return writeErrorResult(out, err.Error())
	}
	orderConfigurations(result, loadResult)

	return writeSuccessJSON(out, result, upResultKeys...)
}

// mutate-lane: TestParityMatrix - up.update-uid-remote-user-from-image-metadata-success,
// up.compose-features-staging-layout-success and up.image-post-create-remote-env-success
// read the remote user and the merged configuration through it.
func (r *upRunner) imageLabels(ctx context.Context, ref string) map[string]string {
	if ref == "" {
		return nil
	}
	img, err := r.engine.InspectImage(ctx, ref)
	if err != nil || img.Config == nil {
		return nil
	}
	return img.Config.Labels
}

func upRemoteUser(cfgUser string, ctr container.InspectResponse, imageLabels func(ref string) map[string]string, logger log.Logger) string {
	metadataUser := func(labels map[string]string) string {
		return imagemeta.MergeConfiguration(imagemeta.ReadMetadataFromLabels(labels, logger)).RemoteUser
	}
	if cfgUser != "" {
		return cfgUser
	}
	var ctrLabels map[string]string
	ctrUser := ""
	if ctr.Config != nil {
		ctrLabels, ctrUser = ctr.Config.Labels, ctr.Config.User
	}
	if user := metadataUser(ctrLabels); user != "" {
		return user
	}
	if user := metadataUser(imageLabels(ctr.Image)); user != "" {
		return user
	}
	return cmp.Or(ctrUser, "root")
}

func upConfigFileFromArgument(opts *upOpts) bool {
	return opts.configPath != "" || opts.workspaceFolder == "" && opts.overrideConfig != ""
}

func upConfigFileFSPathRead(opts *upOpts, lockfileRead bool) bool {
	readDocument := upConfigFileFromArgument(opts) && (opts.configPath == "" || opts.overrideConfig == "")
	return readDocument || len(opts.idLabels) == 0 || lockfileRead
}

func addUpConfiguration(result map[string]any, opts *upOpts, fsPathRead bool, cfg *config.DevContainer, ctr container.InspectResponse, metadata func(ctr container.InspectResponse, cfg *config.DevContainer) ([]imagemeta.Entry, error), logger log.Logger) error {
	if !opts.includeConfig && !opts.includeMergedConfig {
		return nil
	}
	var containerEnv map[string]string
	if ctr.Config != nil {
		containerEnv = config.EnvMap(ctr.Config.Env)
	}

	if opts.includeConfig {
		cfgJSON, _ := json.Marshal(cfg)
		var cfgGeneric any
		json.Unmarshal(cfgJSON, &cfgGeneric)
		substituted, subErr := resolveContainerVariables(containerEnv, cfg.ConfigFilePath, cfgGeneric)
		if subErr != nil {
			return fmt.Errorf("resolve configuration container variables: %w", subErr)
		}
		if subMap, ok := substituted.(map[string]any); ok {
			for k, v := range subMap {
				if v == nil {
					delete(subMap, k)
				}
			}
			if cfp, ok := subMap["configFilePath"].(string); ok && cfp != "" {
				scheme := "vscode-fileHost"
				if upConfigFileFromArgument(opts) {
					scheme = "file"
				}
				subMap["configFilePath"] = configFileURI(cfp, scheme, fsPathRead)
			} else {
				delete(subMap, "configFilePath")
			}
			delete(subMap, "extensions")
			delete(subMap, "settings")
			delete(subMap, "devPort")
			result["configuration"] = subMap
		} else {
			result["configuration"] = substituted
		}
	}

	if opts.includeMergedConfig {
		entries, err := metadata(ctr, cfg)
		if err != nil {
			return err
		}
		merged := imagemeta.MergeConfiguration(entries)
		merged.Image = cfg.Image

		mergedJSON, _ := json.Marshal(merged)
		var mergedGeneric any
		json.Unmarshal(mergedJSON, &mergedGeneric)
		mergedSub, subErr := resolveContainerVariables(containerEnv, cfg.ConfigFilePath, mergedGeneric)
		if subErr != nil {
			return fmt.Errorf("resolve merged container variables: %w", subErr)
		}
		cfgResult, _ := result["configuration"].(map[string]any)
		if mm, ok := mergedSub.(map[string]any); ok {
			for k, v := range cfgResult {
				if !mergedOwnKeys[k] {
					mm[k] = v
				}
			}
		}
		result["mergedConfiguration"] = mergedSub
	}
	return nil
}

// mutate-lane: TestParityMatrix - up.update-uid-remote-user-from-image-metadata-success,
// up.dockerfile-without-features-omit-config-remote-env-from-metadata-success,
// up.dockerfile-with-parallel-commands-success, up.update-uid-amd64-dockerfile-platform-success
// and up.read-only-workspace-dockerfile-success build through it; also TestUpDockerfileBuildsOnce,
// TestE2E_BuildUpExec_Dockerfile and
// TestE2E_UpSecretsFile (a Dockerfile RUN reads a --secrets-file secret).
func (r *upRunner) fromDockerfile(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	opts := r.opts
	imageName := folderImageName(workspaceFolder)
	if len(cfg.Features) > 0 {
		r.lockfileRead = !opts.noLockfile
	}
	fb := r.featureBuildOptions(cfg, loadResult, "")
	fb.NoCache, fb.CacheFrom, fb.CacheTo = opts.buildNoCache, opts.cacheFrom, opts.cacheTo
	fb.OmitSyntaxDirective = opts.omitSyntaxDirective
	if err := buildDockerfileImage(ctx, r.log, r.docker, r.engine, cfg, useBuildx, []string{imageName}, opts.platform, fb); err != nil {
		return "", err
	}

	imageName, err := r.singleContainerRemoteUserUID(ctx, imageName, cfg, workspaceFolder)
	if err != nil {
		return "", err
	}

	return r.runContainer(ctx, imageName, cfg, loadResult, workspaceFolder, idLabels, true)
}

// mutate-lane: TestParityMatrix - up.image-success, up.image-with-features-success,
// up.image-with-git-feature-success and up.update-uid-amd64-platform-option-success
// start through it; also TestUpImageFeaturesTagsFolderImage, TestE2E_BuildUpExec_Image and
// TestE2E_FeatureUserHomeAndLocalEnv.
func (r *upRunner) fromImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return "", fmt.Errorf("no image specified in devcontainer.json")
	}

	platform := opts.platform
	if platform == "" {
		platform = findPlatformArg(cfg.RunArgs)
	}

	_, err := engine.InspectImage(ctx, cfg.Image)
	if err != nil {
		logger.Write(fmt.Sprintf("Pulling image %s...", cfg.Image), log.LevelInfo)
		if pullErr := engine.PullImagePlatform(ctx, cfg.Image, platform); pullErr != nil {
			return "", fmt.Errorf("pull image %q: %w", cfg.Image, pullErr)
		}
	}

	imageName := cfg.Image
	if len(cfg.Features) > 0 {
		r.lockfileRead = !opts.noLockfile
		names, err := extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, []string{folderImageName(workspaceFolder) + "-features"}, r.featureBuildOptions(cfg, loadResult, platform))
		if err != nil {
			return "", fmt.Errorf("install features: %w", err)
		}
		if len(names) > 0 {
			imageName = names[0]
		}
	}

	imageName, err = r.singleContainerRemoteUserUID(ctx, imageName, cfg, workspaceFolder)
	if err != nil {
		return "", err
	}

	return r.runContainer(ctx, imageName, cfg, loadResult, workspaceFolder, idLabels, len(cfg.Features) > 0)
}

// mutate-lane: TestE2E_UpCacheImage - starts a container from a local prebuilt
// image and fails to pull a missing one (--cache-image is Go-only, so no parity
// case covers it).
func (r *upRunner) fromCacheImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string) (string, error) {
	logger, engine := r.log, r.engine
	image := r.opts.cacheImage

	if _, err := engine.InspectImage(ctx, image); err != nil {
		logger.Write(fmt.Sprintf("Pulling cache image %s...", image), log.LevelInfo)
		if pullErr := engine.PullImage(ctx, image); pullErr != nil {
			return "", fmt.Errorf("pull cache image %q: %w", image, pullErr)
		}
	}
	logger.Write(fmt.Sprintf("Using prebuilt cache image %s; skipping build and feature install", image), log.LevelInfo)

	image, err := r.singleContainerRemoteUserUID(ctx, image, cfg, workspaceFolder)
	if err != nil {
		return "", err
	}
	_, configInImage := r.imageLabels(ctx, image)[imagemeta.MetadataLabel]
	return r.runContainer(ctx, image, cfg, loadResult, workspaceFolder, idLabels, configInImage)
}

func (r *upRunner) singleContainerRemoteUserUID(ctx context.Context, imageName string, cfg *config.DevContainer, workspaceFolder string) (string, error) {
	imageDetails := func() (image.InspectResponse, error) { return r.engine.InspectImage(ctx, imageName) }
	var entries []imagemeta.Entry
	if details, err := imageDetails(); err == nil && details.Config != nil {
		entries = imagemeta.ReadMetadataFromLabels(details.Config.Labels, r.log)
	}
	merged := imagemeta.MergeConfiguration(append(entries, configMetadataEntry(cfg, r.opts.omitConfigRemoteEnvFromMeta)))
	user := cmp.Or(findUserArg(cfg.RunArgs), merged.ContainerUser)
	return r.updateRemoteUserUID(ctx, merged, imageName, imageDetails, user, workspaceFolder, r.persistedFolder())
}

func findUserArg(runArgs []string) string {
	for i := len(runArgs) - 1; i >= 0; i-- {
		arg := runArgs[i]
		if (arg == "-u" || arg == "--user") && i+1 < len(runArgs) {
			return runArgs[i+1]
		}
		for _, p := range []string{"-u=", "--user="} {
			if v, ok := strings.CutPrefix(arg, p); ok {
				return v
			}
		}
	}
	return ""
}

// mutate-lane: TestParityMatrix - up.image-success, up.app-port-success,
// up.override-command-false-success, up.workspace-mount-custom-success,
// up.image-with-mounts-success and up.config-without-workspace-success create
// their container through it; also TestE2E_ReadonlyMountObject and
// TestE2E_CABundle (the bundle's mount and variables).
func (r *upRunner) runContainer(ctx context.Context, imageName string, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, configInImage bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	imageInspect, _ := engine.InspectImage(ctx, imageName)

	var labelEntries []imagemeta.Entry
	if imageInspect.Config != nil {
		labelEntries = imagemeta.ReadMetadataFromLabels(imageInspect.Config.Labels, logger)
	}
	metaEntries := slices.Clone(labelEntries)
	if err := r.substitute(&metaEntries); err != nil {
		return "", fmt.Errorf("substitute the image's metadata: %w", err)
	}
	containerLabels := append([]string{}, idLabels...)
	if !configInImage {
		metaEntries = append(metaEntries, configMetadataEntry(cfg, opts.omitConfigRemoteEnvFromMeta))
		containerLabels = append(containerLabels, imagemeta.MetadataLabel+"="+containerMetadataLabel(append(labelEntries, r.configLabel)))
	}
	merged := imagemeta.MergeConfiguration(metaEntries)

	cmdScript := docker.BuildEntrypointScript(merged.Entrypoints)

	containerUser := containerRunUser(merged.ContainerUser, imageInspect)

	var envVars []string
	for k, v := range withCABundleEnv(merged.ContainerEnv, opts.caBundle) {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	mounts, err := upWorkspaceMounts(loadResult.WorkspaceConfig, workspaceFolder, opts.workspaceMountConsistency)
	if err != nil {
		return "", err
	}
	devcontainerID := config.ComputeDevContainerID(config.EnvMap(idLabels))
	if len(merged.Mounts) > 0 {
		metadataMounts, err := mountsFromMetadata(merged.Mounts, devcontainerID)
		if err != nil {
			return "", fmt.Errorf("resolve mounts: %w", err)
		}
		mounts = append(mounts, metadataMounts...)
	}
	flagMounts, err := parseFlagMounts(opts.mounts)
	if err != nil {
		return "", err
	}
	mounts = append(mounts, flagMounts...)
	if opts.caBundle != "" {
		mounts = append(mounts, caBundleMount(opts.caBundle))
	}

	runArgs := cfg.RunArgs
	if cfg.HostRequirements != nil && gpuRequested(cfg.HostRequirements.GPU) {
		gpuAvail := checkGPUAvailability(ctx, opts.gpuAvailability, dockerClient)
		if gpuAvail {
			runArgs = append([]string{"--gpus", "all"}, runArgs...)
		}
	}
	runArgs = append(runArgs, appPortPublishArgs(cfg.AppPort)...)

	cmdArgs := []string{"-c", cmdScript, "-"}
	if merged.OverrideCommand != nil && !*merged.OverrideCommand && imageInspect.Config != nil {
		cmdArgs = append(cmdArgs, imageInspect.Config.Entrypoint...)
		cmdArgs = append(cmdArgs, imageInspect.Config.Cmd...)
	}

	createArgs := docker.CreateContainerArgs(
		imageName,
		containerLabels,
		envVars,
		mounts,
		containerUser,
		[]string{"/bin/sh"},
		cmdArgs,
		merged.CapAdd,
		merged.SecurityOpt,
		merged.Privileged != nil && *merged.Privileged,
		merged.Init,
		runArgs,
	)

	logger.Write(fmt.Sprintf("Starting container from %s...", imageName), log.LevelInfo)

	result, err := dockerClient.Run(ctx, createArgs...)
	if err != nil {
		return "", fmt.Errorf("docker create: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("docker create failed (exit %d): %s", result.ExitCode, string(result.Stderr))
	}

	containerID := strings.TrimSpace(string(result.Stdout))

	if err := engine.StartContainer(ctx, containerID); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	logger.Write(fmt.Sprintf("Container started: %s", containerID[:12]), log.LevelInfo)
	return containerID, nil
}

func containerRunUser(metadataUser string, img image.InspectResponse) string {
	imageUser := ""
	if img.Config != nil {
		imageUser = img.Config.User
	}
	return cmp.Or(metadataUser, imageUser)
}

func upWorkspaceMounts(wc *config.WorkspaceConfig, workspaceFolder, consistency string) ([]dockermount.Mount, error) {
	var mounts []dockermount.Mount
	if spec := wc.WorkspaceMount; !wc.NoWorkspaceMount && (spec != "" || workspaceFolder != "") {
		wsMount := dockermount.Mount{Type: dockermount.TypeBind, Source: workspaceFolder, Target: wc.WorkspaceFolder}
		if spec != "" {
			parsed, parseErr := docker.ParseMountSpec(spec)
			if parseErr != nil {
				return nil, fmt.Errorf("invalid workspaceMount %q: %w", spec, parseErr)
			}
			wsMount = parsed
		}
		mounts = append(mounts, wsMount)
	}
	for _, spec := range wc.AdditionalMounts {
		parsed, parseErr := docker.ParseMountSpec(spec)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid additional mount %q: %w", spec, parseErr)
		}
		mounts = append(mounts, parsed)
	}
	if consistency != "" {
		for i := range mounts {
			mounts[i].Consistency = dockermount.Consistency(consistency)
		}
	}
	return mounts, nil
}

func parseFlagMounts(specs []string) ([]dockermount.Mount, error) {
	var mounts []dockermount.Mount
	for _, m := range specs {
		parsed, parseErr := docker.ParseMountSpec(m)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid --mount %q: %w", m, parseErr)
		}
		mounts = append(mounts, parsed)
	}
	return mounts, nil
}

func resolveComposeProjectName(ctx context.Context, cfg *config.DevContainer, env map[string]string, composeFiles []string, composeClient *docker.ComposeClient) string {
	newNames := composeClient.UsesNewProjectNames()

	if envName, ok := env["COMPOSE_PROJECT_NAME"]; ok && envName != "" {
		return docker.ToProjectName(envName, newNames)
	}

	configDir := filepath.Dir(cfg.ConfigFilePath)
	if data, err := os.ReadFile(filepath.Join(configDir, ".env")); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if after, ok := strings.CutPrefix(line, "COMPOSE_PROJECT_NAME="); ok {
				val := strings.TrimSpace(after)
				if val != "" {
					return docker.ToProjectName(val, newNames)
				}
			}
		}
	}

	if composeConfig, configErr := composeClient.Config(ctx, composeFiles, ""); configErr == nil {
		if name, ok := composeConfig["name"].(string); ok && name != "" {
			if name != "devcontainer" {
				return docker.ToProjectName(name, newNames)
			}
			for _, composeFile := range slices.Backward(composeFiles) {
				raw, readErr := os.ReadFile(composeFile)
				if readErr == nil && strings.Contains(string(raw), "name:") {
					return docker.ToProjectName(name, newNames)
				}
			}
		}
	}

	workingDir := filepath.Dir(composeFiles[0])
	if filepath.Base(configDir) == ".devcontainer" {
		return docker.ToProjectName(filepath.Base(filepath.Dir(configDir))+"_devcontainer", newNames)
	}
	return docker.ToProjectName(filepath.Base(workingDir), newNames)
}

type composeServiceBuild struct {
	Image      string
	HasBuild   bool
	Dockerfile string
	Context    string
	Target     string
	Args       map[string]string
}

func parseComposeServiceBuild(composeConfig map[string]any, service string) composeServiceBuild {
	var out composeServiceBuild
	services, ok := composeConfig["services"].(map[string]any)
	if !ok {
		return out
	}
	svc, ok := services[service].(map[string]any)
	if !ok {
		return out
	}
	if img, ok := svc["image"].(string); ok {
		out.Image = img
	}
	build, ok := svc["build"].(map[string]any)
	if !ok {
		return out
	}
	out.HasBuild = true
	if df, ok := build["dockerfile"].(string); ok {
		out.Dockerfile = df
	}
	if bctx, ok := build["context"].(string); ok {
		out.Context = bctx
	}
	if tgt, ok := build["target"].(string); ok {
		out.Target = tgt
	}
	if args, ok := build["args"].(map[string]any); ok {
		out.Args = make(map[string]string, len(args))
		for k, v := range args {
			out.Args[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// mutate-lane: TestParityMatrix - up.compose-with-name-success,
// up.compose-image-without-features-success, up.compose-dockerfile-with-features-success,
// up.compose-dockerfile-with-features-reuse-stopped-container-with-persisted-overrides-success
// and up.compose-image-with-mounts-success start their project through it.
func (r *upRunner) fromCompose(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Service == "" {
		return "", fmt.Errorf("dockerComposeFile config requires 'service' property")
	}

	env := osEnvMap()
	composeFiles, err := config.DockerComposeFilePaths(cfg, env, filepath.Dir(cfg.ConfigFilePath))
	if err != nil {
		return "", fmt.Errorf("resolve compose files: %w", err)
	}

	composeClient, err := docker.NewComposeClient(opts.dockerPath, opts.dockerComposePath, nil, logger)
	if err != nil {
		return "", fmt.Errorf("compose client: %w", err)
	}
	composeClient.ProgressWriter = dockerClient.ProgressWriter

	composeConfig, err := composeClient.Config(ctx, composeFiles, "")
	if err != nil {
		return "", err
	}
	projectName := resolveComposeProjectName(ctx, cfg, env, composeFiles, composeClient)
	logger.Write(fmt.Sprintf("Compose project: %s, service: %s", projectName, cfg.Service), log.LevelInfo)
	opts.composeProjectName = projectName

	existingComposeIDs, err := engine.ListContainers(ctx, true, []string{
		fmt.Sprintf("com.docker.compose.project=%s", projectName),
		fmt.Sprintf("com.docker.compose.service=%s", cfg.Service),
	})
	if err != nil {
		return "", fmt.Errorf("list compose containers: %w", err)
	}
	if opts.expectExistingContainer && len(existingComposeIDs) == 0 {
		return "", fmt.Errorf("the expected container does not exist")
	}
	var existing *container.InspectResponse
	if len(existingComposeIDs) > 0 {
		inspect, err := engine.InspectContainer(ctx, existingComposeIDs[0])
		if err != nil {
			return "", fmt.Errorf("inspect compose container: %w", err)
		}
		existing = &inspect
	}
	if existing != nil && existing.State != nil && existing.State.Running {
		return existing.ID, nil
	}
	if _, ok := composeService(composeConfig, cfg.Service); !ok {
		return "", fmt.Errorf("Service '%s' configured in devcontainer.json not found in Docker Compose configuration.", cfg.Service)
	}

	containerID, err := r.startComposeContainer(ctx, composeStart{
		cfg:             cfg,
		loadResult:      loadResult,
		workspaceFolder: workspaceFolder,
		idLabels:        idLabels,
		useBuildx:       useBuildx,
		projectName:     projectName,
		composeFiles:    composeFiles,
		composeConfig:   composeConfig,
		client:          composeClient,
		container:       existing,
	})
	if err != nil {
		return "", err
	}
	logger.Write(fmt.Sprintf("Compose container: %s", containerID[:12]), log.LevelInfo)
	return containerID, nil
}

// mutate-lane: TestParityMatrix - up.image-post-create-remote-env-success,
// up.dockerfile-with-parallel-commands-success, up.feature-hook-failure-error,
// up.parallel-hook-failure-error, up.workspace-secrets-success and the
// up.dotfiles-local-* cases run their hooks through it.
func (r *upRunner) runLifecycleForUp(ctx context.Context, containerID string, cfg *config.DevContainer, workDir string, remoteUser string) error {
	logger, engine, opts := r.log, r.engine, r.opts
	probeStrategy := lifecycle.UserEnvProbeStrategy(cmp.Or(cfg.UserEnvProbe, opts.defaultUserEnvProbe))
	probed := lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, remoteUser, probeStrategy, opts.containerSessionDataFolder)
	mergedRemoteEnv := hookRemoteEnv(probed, opts.remoteEnvs, cfg.RemoteEnv, opts.secrets.env())

	shellServer := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, mergedRemoteEnv...)
	defer shellServer.Close()

	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger, WorkDir: workDir}

	inspectResp, _ := engine.InspectContainer(ctx, containerID)
	entries, err := r.metadataOfContainer(inspectResp, cfg)
	if err != nil {
		return hookContainerError(err, containerID, "An error occurred setting up the container.")
	}
	merged := imagemeta.MergeConfiguration(entries)

	applyLifecycleMarkers(shellServer, merged, inspectResp, opts.prebuild)

	hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
		SkipNonBlocking: opts.skipNonBlocking,
		SkipPostAttach:  opts.skipPostAttach,
		Prebuild:        opts.prebuild,
		AfterPostCreate: dotfilesAfterPostCreate(logger, executor, lifecycle.DotfilesConfig{
			Repository:     opts.dotfilesRepo,
			InstallCommand: opts.dotfilesCommand,
			TargetPath:     opts.dotfilesTarget,
		}),
	})
	if hookErr != nil {
		return hookContainerError(hookErr, containerID, "An error occurred setting up the container.")
	}
	return nil
}

// mutate-lane: TestParityMatrix - up.container-env-without-name-error fails
// up through it against a real container.
func (r *upRunner) checkContainerSubstitution(inspectResp container.InspectResponse, cfg *config.DevContainer, raw map[string]any) error {
	entries, err := r.metadataOfContainer(inspectResp, cfg)
	if err != nil {
		return err
	}
	var containerEnv map[string]string
	if inspectResp.Config != nil {
		containerEnv = config.EnvMap(inspectResp.Config.Env)
	}
	return checkContainerSubstitution(containerEnv, cfg.ConfigFilePath, raw, imagemeta.MergeConfiguration(entries))
}

func hookRemoteEnv(probed map[string]string, remoteEnvs []string, cfgRemoteEnv map[string]*string, secrets []string) []string {
	var env []string
	for k, v := range probed {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = append(env, remoteEnvs...)
	for k, v := range cfgRemoteEnv {
		if v != nil {
			env = append(env, fmt.Sprintf("%s=%s", k, *v))
		}
	}
	return append(env, secrets...)
}

type containerRemover interface {
	RemoveContainer(ctx context.Context, id string) error
}

type containerRemoveError struct {
	dockerPath string
	id         string
	err        error
}

func (e *containerRemoveError) Error() string {
	return fmt.Sprintf("Command failed: %s rm -f %s", e.dockerPath, e.id)
}

func (e *containerRemoveError) Unwrap() error { return e.err }

func removeExistingContainer(ctx context.Context, engine containerRemover, logger log.Logger, cfg *config.DevContainer, dockerPath, id string) error {
	text := "Removing Existing Container"
	if cfg.IsComposeConfig() {
		text = "Removing existing container."
	}
	start := logger.Start(text)
	if err := engine.RemoveContainer(ctx, id); err != nil {
		removeErr := &coreerrors.ContainerError{
			Description:   "An error occurred setting up the container.",
			OriginalError: &containerRemoveError{dockerPath: cmp.Or(dockerPath, "docker"), id: id, err: err},
		}
		if cfg.IsComposeConfig() {
			removeErr.ContainerID = id
		}
		return removeErr
	}
	logger.Stop(text, start)
	return nil
}

func hookContainerError(hookErr error, containerID, fallback string) *coreerrors.ContainerError {
	he, ok := errors.AsType[*lifecycle.HookError](hookErr)
	if !ok {
		return &coreerrors.ContainerError{Description: fallback, OriginalError: hookErr, ContainerID: containerID}
	}
	return &coreerrors.ContainerError{Description: he.Description(), OriginalError: he.Err, ContainerID: containerID}
}

// mutate-lane: TestParityMatrix - set-up.after-failed-post-create-success (set-up
// after a failed postCreateCommand does not rerun the hooks whose marker was
// written) and up.dockerfile-with-parallel-commands-success run it against a
// real container; dropRanHooks holds its decisions.
func applyLifecycleMarkers(server *lifecycle.ShellServer, merged *imagemeta.MergedConfig, inspect container.InspectResponse, prebuild bool) {
	folder := resolveContainerUserDataFolder(server)
	if folder == "" {
		return
	}
	dropRanHooks(merged, inspect, prebuild, func(hook, timestamp string) bool {
		return markerShouldRun(server, folder, hook, timestamp)
	})
}

func dropRanHooks(merged *imagemeta.MergedConfig, inspect container.InspectResponse, prebuild bool, shouldRun func(hook, timestamp string) bool) {
	startedAt := ""
	if inspect.State != nil {
		startedAt = inspect.State.StartedAt
	}
	if !shouldRun("onCreateCommand", inspect.Created) {
		merged.OnCreateCommands = nil
	}
	if !shouldRun("updateContentCommand", inspect.Created) && !prebuild {
		merged.UpdateContentCommands = nil
	}
	if !shouldRun("postCreateCommand", inspect.Created) {
		merged.PostCreateCommands = nil
	}
	if !shouldRun("postStartCommand", startedAt) {
		merged.PostStartCommands = nil
	}
}

func dotfilesAfterPostCreate(logger log.Logger, executor lifecycle.CommandExecutor, cfg lifecycle.DotfilesConfig) func() {
	return func() {
		if err := lifecycle.InstallDotfiles(logger, executor, cfg); err != nil {
			logger.Write(fmt.Sprintf("Dotfiles installation failed: %v", err), log.LevelWarning)
		}
	}
}

func resolveContainerUserDataFolder(server *lifecycle.ShellServer) string {
	home, code, err := server.Exec(`printf %s "$HOME"`)
	home = strings.TrimSpace(home)
	if err != nil || code != 0 || home == "" {
		return ""
	}
	return home + "/.devcontainer"
}

func markerShouldRun(server *lifecycle.ShellServer, folder, hookName, content string) bool {
	if content == "" {
		return true
	}
	marker := folder + "/." + hookName + "Marker"
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	cmd := fmt.Sprintf(`mkdir -p %s && CONTENT="$(cat %s 2>/dev/null || echo ENOENT)" && [ "${CONTENT:-%s}" != %s ] && printf '%%s\n' %s > %s`,
		q(folder), q(marker), content, q(content), q(content), q(marker))
	_, code, err := server.Exec(cmd)
	return err == nil && code == 0
}

func (r *upRunner) featureBuildOptions(cfg *config.DevContainer, loadResult *config.LoadResult, platform string) *FeatureBuildOptions {
	opts := r.opts
	return &FeatureBuildOptions{
		Platform:                    platform,
		Secrets:                     opts.secrets.env(),
		CABundle:                    opts.caBundle,
		OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
		FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
		WorkspaceFolder:             opts.workspaceFolder,
		FrozenLockfile:              opts.experimentalFrozenLockfile,
		NoLockfile:                  opts.noLockfile,
		ConfigPath:                  cfg.ConfigFilePath,
		LockfileExcludeIDs:          opts.lockfileExcludeIDs,
		HostSub:                     &loadResult.HostSub,
		ContainerEnv:                cfg.ContainerEnv,
		ContainerEnvOrder:           cfg.ContainerEnvOrder,
		FeatureOptionOrder:          cfg.FeatureOptionOrder,
		AdditionalMetadata:          []imagemeta.Entry{configMetadataEntry(cfg, false)},
		LabelMetadata:               []imagemeta.Entry{r.configLabel},
	}
}

// mutate-lane: TestParityMatrix - up.feature-hook-failure-error and
// up.dockerfile-with-parallel-commands-success run the hooks it reads from a
// real container; also TestDifferentialUp.
func (r *upRunner) metadataOfContainer(ctr container.InspectResponse, cfg *config.DevContainer) ([]imagemeta.Entry, error) {
	var labels map[string]string
	if ctr.Config != nil {
		labels = ctr.Config.Labels
	}
	return imageMetadataFromContainer(labels, cfg, nil, r.idLabels, r.substitute, r.log)
}
