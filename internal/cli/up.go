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
	workspaceFolder string
	configPath      string
	overrideConfig  string
	// lockfileExcludeIDs lists userFeatureIds supplied only via --additional-features;
	// 0.88 keeps these out of the lockfile.
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
	terminalColumns             int
	terminalRows                int
	secretsFile                 string
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
	skipFeatureAutoMapping      bool
	// Set by upFromCompose, read when building result
	composeProjectName string
}

// upRunner carries the dependencies shared across the whole `up` flow. docker is
// nil on the reattach path (set once BuildKit detection has run). The context is
// passed explicitly as the first argument of each method, not stored here, so
// cancellation is visible in every signature that does I/O.
type upRunner struct {
	out    Output
	log    log.Logger
	docker *docker.Client
	engine *docker.EngineClient
	opts   *upOpts
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
	// Go-only: start from a prebuilt image (features already baked in) instead of
	// building + installing features. The image's devcontainer.metadata label
	// supplies the merged configuration.
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
	f.BoolVar(&opts.skipFeatureAutoMapping, "skip-feature-auto-mapping", false, "")
	f.StringVar(&opts.dotfilesRepo, "dotfiles-repository", "", "Dotfiles repo.")
	f.StringVar(&opts.dotfilesCommand, "dotfiles-install-command", "", "Dotfiles install command.")
	f.StringVar(&opts.dotfilesTarget, "dotfiles-target-path", "~/dotfiles", "Dotfiles target.")
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "Session data folder.")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "Secrets file path.")
	f.BoolVar(&opts.experimentalLockfile, "experimental-lockfile", false, "")
	f.BoolVar(&opts.experimentalFrozenLockfile, "experimental-frozen-lockfile", false, "")
	f.BoolVar(&opts.noLockfile, "no-lockfile", false, "Disable lockfile generation and verification.")
	f.BoolVar(&opts.frozenLockfile, "frozen-lockfile", false, "Ensure lockfile exists and remains unchanged; fail otherwise.")
	f.BoolVar(&opts.mountGitWorktreeCommonDir, "mount-git-worktree-common-dir", false, "Mount the Git worktree common dir for Git operations to work in the container (requires the worktree created with `git worktree add --relative-paths`).")
	f.BoolVar(&opts.omitSyntaxDirective, "omit-syntax-directive", false, "")
	f.BoolVar(&opts.omitConfigRemoteEnvFromMeta, "omit-config-remote-env-from-metadata", false, "")
	// Hidden experimental/testing flags (match the TS CLI's hidden: true).
	for _, h := range []string{
		"skip-feature-auto-mapping",
		"experimental-lockfile",
		"experimental-frozen-lockfile",
		"omit-syntax-directive",
		"omit-config-remote-env-from-metadata",
	} {
		_ = f.MarkHidden(h)
	}
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")
	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)

	return cmd
}

// validateUpOpts checks the up command's flag values, returning the first
// invalid one (nil if all are valid). Enum choices mirror the TS CLI.
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

// resolveUpPaths resolves the workspace/config/override paths to absolute form.
// Each result is empty when its corresponding flag was not set.
func resolveUpPaths(opts *upOpts) (workspaceFolder, configPath, overridePath string) {
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

func runUp(ctx context.Context, out Output, opts *upOpts) error {
	if err := validateUpOpts(opts); err != nil {
		return writeValidationError(out, err.Error())
	}
	// --no-lockfile is mutually exclusive with every frozen/write lockfile flag
	// (matching the TS CLI up command's yargs check).
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
	// TS folds --experimental-frozen-lockfile into --frozen-lockfile.
	opts.experimentalFrozenLockfile = opts.experimentalFrozenLockfile || opts.frozenLockfile
	// Non-blocking hint (interactive TTY only) if the host was never checked or a
	// previous `devcontainer check` found a failing configuration.
	warnUncheckedHost(out)
	// 0.88: default --workspace-folder to the current directory when neither
	// --workspace-folder, --id-label nor --override-config is given.
	if opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.overrideConfig == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}

	workspaceFolder, configPath, overridePath := resolveUpPaths(opts)

	logDst, closeLog, logErr := logWriter(opts.logFile, opts.terminalLogFile)
	if logErr != nil {
		return writeErrorResult(out, fmt.Sprintf("open log file: %v", logErr))
	}
	defer closeLog()

	logger := log.New(log.Options{
		Version: cliVersion(),
		Level:   log.ParseLevel(opts.logLevel),
		Format:  opts.logFormat,
		Writer:  logDst,
		Secrets: secretValuesFromFile(opts.secretsFile),
	})
	// Route the OCI auth diagnostic lines at this command's logger.
	ociAuthPolicy(ctx).SetLogger(logger)

	// Engine SDK client for container/image operations
	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	run := &upRunner{out: out, log: logger, engine: engine, opts: opts}

	// When --id-label is provided, use those labels directly for container lookup.
	// Otherwise, derive labels from workspace folder + config file path.
	idLabels := opts.idLabels

	// TS reads the config before it looks for a container (configContainer.ts
	// resolveWithLocalFolder): with neither a workspace nor a config there is
	// nothing to read, even when --id-label finds a container.
	if workspaceFolder == "" && configPath == "" && overridePath == "" {
		return writeErrorResult(out, "No dev container config and no workspace found.")
	}

	// Load config
	loadResult, err := config.LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overridePath, config.MountOptions{
		MountWorkspaceGitRoot:     opts.mountWorkspaceGitRoot,
		MountGitWorktreeCommonDir: opts.mountGitWorktreeCommonDir,
	})
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	cfg := loadResult.Config

	// Derive id labels before consuming configuration fields so the complete
	// pre-container pipeline can run first.
	if len(idLabels) == 0 {
		idLabels = []string{
			fmt.Sprintf("devcontainer.local_folder=%s", workspaceFolder),
		}
		if loadResult.Config.ConfigFilePath != "" {
			idLabels = append(idLabels, fmt.Sprintf("devcontainer.config_file=%s", loadResult.Config.ConfigFilePath))
		}
	}

	// Merge --additional-features into config (config features have priority)
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

	// Apply --remove-existing-container up-front, BEFORE initializeCommand, so a
	// failed initializeCommand (or rebuild) does not leave the old container
	// running — matching the VS Code extension (upstream #844). TS removes it while
	// finding the id labels, before the disallowed-Feature check.
	existingIDs, _ := engine.ListContainers(ctx, true, idLabels)
	if len(existingIDs) > 0 && opts.removeExisting {
		logger.Write(fmt.Sprintf("Removing existing container %s...", existingIDs[0]), log.LevelInfo)
		_ = engine.RemoveContainer(ctx, existingIDs[0])
		existingIDs = nil
	}

	if derr := enforceDisallowedFeatures(ctx, cfg, opts.additionalFeatures, logger, stopRunningContainer(engine, idLabels)); derr != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(derr))
	}

	// Run initializeCommand on the host (before container creation).
	// A failure aborts `up` with an error outcome, matching the TS CLI (which
	// throws a ContainerError). Swallowing it produced silently-broken setups.
	if !cfg.InitializeCommand.IsEmpty() {
		if err := lifecycle.RunInitializeCommand(ctx, logger, &cfg.InitializeCommand, loadResult.HostSub); err != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: "The initializeCommand in the devcontainer.json failed.",
			}))
		}
	}

	dockerClient := docker.NewClient(opts.dockerPath, nil, logger)
	// Stream `docker build` progress live to the log destination (text mode only;
	// under --log-format json it would corrupt the structured event stream).
	dockerClient.ProgressWriter = progressWriter(opts.logFormat, logDst)
	run.docker = dockerClient

	// Detect BuildKit
	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit(ctx)
		useBuildx = bk.Available
	}

	var containerID string

	if len(existingIDs) > 0 && !cfg.IsComposeConfig() {
		// Single-container reuse: start the existing container directly.
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
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: fmt.Sprintf("An error occurred setting up the container: %v", err),
			}))
		}
	}

	// Get container info
	inspectResp, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Failed to inspect container: %v", err))
	}

	// Determine remote user: config → container metadata → image metadata →
	// container USER → root
	remoteUser := cfg.RemoteUser
	if remoteUser == "" && inspectResp.Config != nil {
		// Check the container's metadata label for remoteUser.
		metaEntries := imagemeta.ReadMetadataFromLabels(inspectResp.Config.Labels, logger)
		if len(metaEntries) > 0 {
			metaMerged := imagemeta.MergeConfiguration(metaEntries)
			if metaMerged.RemoteUser != "" {
				remoteUser = metaMerged.RemoteUser
			}
		}
	}
	if remoteUser == "" && inspectResp.Image != "" {
		// Fall back to the base image's baked metadata. For compose services the
		// container's own devcontainer.metadata label is a config-derived override
		// that may lack remoteUser, while the base image (e.g. a devcontainers/*
		// image) still carries it in its metadata label.
		if img, imgErr := engine.InspectImage(ctx, inspectResp.Image); imgErr == nil && img.Config != nil {
			metaMerged := imagemeta.MergeConfiguration(imagemeta.ReadMetadataFromLabels(img.Config.Labels, logger))
			if metaMerged.RemoteUser != "" {
				remoteUser = metaMerged.RemoteUser
			}
		}
	}
	if remoteUser == "" {
		if inspectResp.Config != nil {
			remoteUser = inspectResp.Config.User
		}
		if remoteUser == "" {
			remoteUser = "root"
		}
	}

	remoteWorkspaceFolder := loadResult.WorkspaceConfig.WorkspaceFolder

	// Run lifecycle hooks (unless skipped). A hook failure aborts `up` with an
	// error outcome (matching the TS CLI), instead of reporting success.
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
	// TS leaves remoteWorkspaceFolder out when there is none (up --config with no
	// workspace and no workspaceFolder in the config).
	if remoteWorkspaceFolder != "" {
		result["remoteWorkspaceFolder"] = remoteWorkspaceFolder
	}
	if opts.composeProjectName != "" {
		result["composeProjectName"] = opts.composeProjectName
	}

	if opts.includeConfig || opts.includeMergedConfig {
		// Reuse inspectResp from above — no need to re-inspect the same container.
		containerEnv := map[string]string{}
		if inspectResp.Config != nil {
			containerEnv = config.EnvMap(inspectResp.Config.Env)
		}

		if opts.includeConfig {
			cfgJSON, _ := json.Marshal(cfg)
			var cfgGeneric any
			json.Unmarshal(cfgJSON, &cfgGeneric)
			substituted, subErr := resolveContainerVariables(containerEnv, cfgGeneric)
			if subErr != nil {
				return writeErrorResult(out, fmt.Sprintf("resolve configuration container variables: %v", subErr))
			}
			if subMap, ok := substituted.(map[string]any); ok {
				for k, v := range subMap {
					if v == nil {
						delete(subMap, k)
					}
				}
				if cfp, ok := subMap["configFilePath"].(string); ok && cfp != "" {
					scheme := "vscode-fileHost"
					if opts.configPath != "" || opts.overrideConfig != "" {
						scheme = "file"
					}
					subMap["configFilePath"] = map[string]any{
						"$mid":   1,
						"path":   cfp,
						"scheme": scheme,
					}
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
			// Read the image's metadata (features are baked into the image), not the
			// container's — the container now carries the full config metadata as a
			// label, so reading it here and re-adding the config entry would double
			// the lifecycle commands. The output entry uses the config's
			// remoteEnv regardless of --omit-config-remote-env-from-metadata (that
			// flag only affects the persisted label).
			var entries []imagemeta.Entry
			if inspectResp.Image != "" {
				if img, imgErr := engine.InspectImage(ctx, inspectResp.Image); imgErr == nil && img.Config != nil {
					entries = imagemeta.ReadMetadataFromLabels(img.Config.Labels, logger)
				}
			}
			configEntry := configToMetadataEntry(cfg)
			entries = append(entries, configEntry)
			merged := imagemeta.MergeConfiguration(entries)
			// image is spread from the config into mergedConfiguration (TS); it is
			// not part of the metadata entries.
			merged.Image = cfg.Image
			if cfgResult, ok := result["configuration"].(map[string]any); ok {
				if cfpURI, ok := cfgResult["configFilePath"]; ok {
					merged.ConfigFilePath = cfpURI
				}
			}

			// Substitute containerEnv into merged config
			mergedJSON, _ := json.Marshal(merged)
			var mergedGeneric any
			json.Unmarshal(mergedJSON, &mergedGeneric)
			mergedSub, subErr := resolveContainerVariables(containerEnv, mergedGeneric)
			if subErr != nil {
				return writeErrorResult(out, fmt.Sprintf("resolve merged container variables: %v", subErr))
			}
			// Every config property TS mergeConfiguration does not set itself is
			// spread into mergedConfiguration ({...config}).
			if mm, ok := mergedSub.(map[string]any); ok {
				if cfgResult, ok := result["configuration"].(map[string]any); ok {
					for k, v := range cfgResult {
						if !mergedOwnKeys[k] {
							mm[k] = v
						}
					}
				}
			}
			result["mergedConfiguration"] = mergedSub
		}
	}

	return writeSuccessJSON(out, result)
}

func (r *upRunner) fromDockerfile(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts

	prep, err := prepareDockerfileBuild(cfg)
	if err != nil {
		return "", err
	}
	defer prep.Cleanup()
	dockerfilePath := prep.Path
	stageName := prep.StageName
	configDir := filepath.Dir(cfg.ConfigFilePath)

	imageName := folderImageName(workspaceFolder)

	// Base-image resolution uses only an explicit config target (not the
	// auto-added final-stage name), then reads its metadata (remoteUser, etc.).
	findTarget := ""
	if cfg.Build != nil && cfg.Build.Target != "" {
		findTarget = cfg.Build.Target
	}
	baseImage := docker.FindBaseImage(prep.Parsed, buildArgsFromConfig(cfg), findTarget)

	metadata := []imagemeta.Entry{}
	// Read the FROM image's baked devcontainer.metadata (e.g. remoteUser=node on
	// the mcr devcontainers images). The metadata is computed before the build, so
	// on a fresh run the base image is otherwise absent and its metadata would be
	// silently lost — making exec/lifecycle fall back to root.
	baseLabels := engine.ImageLabelsEnsuringPresent(ctx, baseImage)
	metadata = append(metadata, imagemeta.ReadMetadataFromLabels(baseLabels, logger)...)
	// Preserve a `LABEL devcontainer.metadata` from the user's Dockerfile (#1225).
	metadata = append(metadata, imagemeta.ReadMetadataFromLabels(prep.Parsed.StageLabels(stageName), logger)...)
	metadata = append(metadata, imagemeta.Entry{RemoteUser: cfg.RemoteUser, ContainerUser: cfg.ContainerUser})
	metadataLabel := imagemeta.GenerateMetadataLabel(metadata)

	contextPath := configDir
	if cfg.BuildContext() != "" {
		contextPath = filepath.Join(configDir, cfg.BuildContext())
	}

	upCacheFrom := cacheFromForDockerfileBuild(opts.cacheFrom, cfg)
	// Bridge the CLI credential chain to the build subprocess via a temporary
	// DOCKER_CONFIG (private base pull / --cache-to).
	authEnv, authCleanup := bridgeBuildAuth(logger, baseImage, []string{imageName}, upCacheFrom, opts.cacheTo)
	defer authCleanup()

	buildResult, err := dockerClient.Build(ctx, docker.BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: contextPath,
		Tags:        []string{imageName},
		Target:      stageName,
		BuildArgs:   buildArgsFromConfig(cfg),
		ExtraArgs:   buildOptionsFromConfig(cfg),
		CacheFrom:   upCacheFrom,
		Labels:      []string{fmt.Sprintf("%s=%s", imagemeta.MetadataLabel, metadataLabel)},
		NoCache:     opts.buildNoCache,
		UseBuildx:   useBuildx,
		CacheTo:     opts.cacheTo,
		Env:         authEnv,
	})
	if err != nil {
		return "", err
	}
	if buildResult.ExitCode != 0 {
		return "", fmt.Errorf(msgDockerBuildFailed, buildResult.ExitCode, string(buildResult.Stderr))
	}

	// When streaming, the build output already reached the terminal live; only
	// emit the buffered copy when it did not (e.g. --log-format json).
	if dockerClient.ProgressWriter == nil {
		logger.Write(string(buildResult.Stderr), log.LevelInfo)
	}

	// Extend with features if any
	if len(cfg.Features) > 0 {
		names, err := extendImageWithFeatures(ctx, logger, dockerClient, engine, imageName, cfg.Features, useBuildx, nil, r.featureBuildOptions(cfg, loadResult, ""))
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

	return r.runContainer(ctx, imageName, cfg, loadResult, workspaceFolder, idLabels)
}

func (r *upRunner) fromImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return "", fmt.Errorf("no image specified in devcontainer.json")
	}

	// Target platform: the --platform flag wins, else a --platform in runArgs, so
	// the image is pulled and (feature-)built for the platform it will run on —
	// e.g. an amd64 image forced via runArgs on an arm64 host (#1241).
	platform := opts.platform
	if platform == "" {
		platform = findPlatformArg(cfg.RunArgs)
	}

	// Pull if needed
	_, err := engine.InspectImage(ctx, cfg.Image)
	if err != nil {
		logger.Write(fmt.Sprintf("Pulling image %s...", cfg.Image), log.LevelInfo)
		if pullErr := engine.PullImagePlatform(ctx, cfg.Image, platform); pullErr != nil {
			return "", fmt.Errorf("pull image %q: %w", cfg.Image, pullErr)
		}
	}

	// Extend with features if any
	imageName := cfg.Image
	if len(cfg.Features) > 0 {
		names, err := extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, nil, r.featureBuildOptions(cfg, loadResult, platform))
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

	return r.runContainer(ctx, imageName, cfg, loadResult, workspaceFolder, idLabels)
}

// fromCacheImage starts the container from a prebuilt image (--cache-image),
// skipping the image build and feature installation entirely. The prebuilt image
// is expected to already carry the features and a devcontainer.metadata label
// (produced by an earlier build of the same configuration), so the merged
// configuration is recovered from that label the same way as a plain image-based
// config. remoteUser/mounts/lifecycle still come from devcontainer.json.
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
	return r.runContainer(ctx, image, cfg, loadResult, workspaceFolder, idLabels)
}

// singleContainerRemoteUserUID is TS singleContainer's updateRemoteUserUID call:
// the merged configuration is the image's metadata and the config's, and the
// user is the runArgs' --user, else the merged containerUser.
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

// findUserArg is TS findUserArg: the last -u/--user of runArgs.
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

func (r *upRunner) runContainer(ctx context.Context, imageName string, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	// Inspect image once — used for metadata and containerUser resolution.
	imageInspect, _ := engine.InspectImage(ctx, imageName)

	var metaEntries []imagemeta.Entry
	if imageInspect.Config != nil {
		metaEntries = append(metaEntries, imagemeta.ReadMetadataFromLabels(imageInspect.Config.Labels, logger)...)
	}
	metaEntries = append(metaEntries, configMetadataEntry(cfg, opts.omitConfigRemoteEnvFromMeta))
	merged := imagemeta.MergeConfiguration(metaEntries)

	// The container carries the full devcontainer.metadata (image + config) so the
	// CLI/VS Code can read remoteUser/lifecycle/customizations back on reattach —
	// image-based containers previously had no metadata label. The label matches
	// what the TS CLI adds with `-l`.
	containerMetadataLabel := imagemeta.GenerateMetadataLabel(metaEntries)

	containerWorkspace := loadResult.WorkspaceConfig.WorkspaceFolder

	// Build CMD script with feature entrypoints
	cmdScript := docker.BuildEntrypointScript(merged.Entrypoints)

	// User: merged containerUser → image USER → omit (docker default)
	containerUser := merged.ContainerUser
	if containerUser == "" && imageInspect.Config != nil && imageInspect.Config.User != "" {
		containerUser = imageInspect.Config.User
	}

	// Container env from the merged metadata (image + config), NOT remote-env
	// (applied at exec/lifecycle time). The merged containerEnv now applies the
	// image metadata's containerEnv too (prebuilds), matching the compose path
	// and the TS CLI. A feature's own containerEnv is NOT in the metadata (it is
	// baked as ENV during the build — see featureMetadataEntry), so this no
	// longer re-applies a literal ${PATH} and breaks PATH.
	var envVars []string
	for k, v := range merged.ContainerEnv {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	// Mounts. Use the computed workspaceMount (which honors a custom
	// `workspaceMount` in the config and the git-root source folder) rather than
	// always binding the workspace folder. Fall back to a bind if unset.
	// With no workspace and no workspaceMount (up --config alone) TS mounts nothing.
	var mounts []dockermount.Mount
	if spec := loadResult.WorkspaceConfig.WorkspaceMount; spec != "" || workspaceFolder != "" {
		wsMount := dockermount.Mount{Type: dockermount.TypeBind, Source: workspaceFolder, Target: containerWorkspace}
		if spec != "" {
			parsed, parseErr := docker.ParseMountSpec(spec)
			if parseErr != nil {
				return "", fmt.Errorf("invalid workspaceMount %q: %w", spec, parseErr)
			}
			wsMount = parsed
		}
		if opts.workspaceMountConsistency != "" {
			wsMount.Consistency = dockermount.Consistency(opts.workspaceMountConsistency)
		}
		mounts = append(mounts, wsMount)
	}
	// Extra workspace-derived mounts (currently the Git worktree common dir from
	// --mount-git-worktree-common-dir) so Git works inside the container.
	for _, spec := range loadResult.WorkspaceConfig.AdditionalMounts {
		parsed, parseErr := docker.ParseMountSpec(spec)
		if parseErr != nil {
			return "", fmt.Errorf("invalid additional mount %q: %w", spec, parseErr)
		}
		if opts.workspaceMountConsistency != "" {
			parsed.Consistency = dockermount.Consistency(opts.workspaceMountConsistency)
		}
		mounts = append(mounts, parsed)
	}
	devcontainerID := config.ComputeDevContainerID(config.EnvMap(idLabels))
	if len(merged.Mounts) > 0 {
		metadataMounts, err := mountsFromMetadata(merged.Mounts, devcontainerID)
		if err != nil {
			return "", fmt.Errorf("resolve mounts: %w", err)
		}
		mounts = append(mounts, metadataMounts...)
	}
	for _, m := range opts.mounts {
		parsed, parseErr := docker.ParseMountSpec(m)
		if parseErr != nil {
			return "", fmt.Errorf("invalid --mount %q: %w", m, parseErr)
		}
		mounts = append(mounts, parsed)
	}

	// GPU support: add --gpus all if config requests GPU and availability allows it.
	// gpu:false must NOT enable GPUs (a raw-length check treated "false" as truthy).
	runArgs := cfg.RunArgs
	if cfg.HostRequirements != nil && gpuRequested(cfg.HostRequirements.GPU) {
		gpuAvail := checkGPUAvailability(ctx, opts.gpuAvailability, dockerClient)
		if gpuAvail {
			runArgs = append([]string{"--gpus", "all"}, runArgs...)
		}
	}
	// appPort → publish flags (127.0.0.1:N:N for numbers, verbatim for strings).
	runArgs = append(runArgs, appPortPublishArgs(cfg.AppPort)...)

	containerLabels := append([]string{}, idLabels...)
	if containerMetadataLabel != "" {
		containerLabels = append(containerLabels, imagemeta.MetadataLabel+"="+containerMetadataLabel)
	}

	// Command: the keep-alive script wrapping any feature entrypoints. When
	// overrideCommand is false, append the image's own entrypoint+cmd so the
	// script's `exec "$@"` runs them (e.g. dind, databases) instead of just
	// sleeping — matching the TS CLI. Default (nil/true) keeps the sleep loop.
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

// resolveComposeProjectName computes the compose project name following the TS
// CLI resolution chain: 1. COMPOSE_PROJECT_NAME env, 2. .env
// COMPOSE_PROJECT_NAME, 3. name: from the compose config, 4. directory-based
// fallback.
func resolveComposeProjectName(ctx context.Context, cfg *config.DevContainer, env map[string]string, composeFiles []string, composeClient *docker.ComposeClient) string {
	newNames := composeClient.UsesNewProjectNames()

	// 1. COMPOSE_PROJECT_NAME env
	if envName, ok := env["COMPOSE_PROJECT_NAME"]; ok && envName != "" {
		return docker.ToProjectName(envName, newNames)
	}

	// 2. .env file (already handled by config.DockerComposeFilePaths, but check here too)
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

	// 3. name: from compose config (via docker compose config)
	if composeConfig, configErr := composeClient.Config(ctx, composeFiles, ""); configErr == nil {
		if name, ok := composeConfig["name"].(string); ok && name != "" {
			if name != "devcontainer" {
				return docker.ToProjectName(name, newNames)
			}
			// "devcontainer" might be the default from docker compose config.
			// Check if it's explicitly in any compose file.
			for _, composeFile := range slices.Backward(composeFiles) {
				raw, readErr := os.ReadFile(composeFile)
				if readErr == nil && strings.Contains(string(raw), "name:") {
					return docker.ToProjectName(name, newNames)
				}
			}
		}
	}

	// 4. Directory-based fallback
	workingDir := filepath.Dir(composeFiles[0])
	if filepath.Base(configDir) == ".devcontainer" {
		return docker.ToProjectName(filepath.Base(filepath.Dir(configDir))+"_devcontainer", newNames)
	}
	return docker.ToProjectName(filepath.Base(workingDir), newNames)
}

// composeServiceBuild captures the image and `build:` fields of a compose
// service that fromCompose needs, parsed once from `docker compose config`
// output instead of re-walking the untyped map at each use.
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

func (r *upRunner) fromCompose(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Service == "" {
		return "", fmt.Errorf("dockerComposeFile config requires 'service' property")
	}

	// Resolve compose files
	env := osEnvMap()
	composeFiles, err := config.DockerComposeFilePaths(cfg, env, filepath.Dir(cfg.ConfigFilePath))
	if err != nil {
		return "", fmt.Errorf("resolve compose files: %w", err)
	}

	// Create compose client
	composeClient, err := docker.NewComposeClient(opts.dockerPath, opts.dockerComposePath, nil, logger)
	if err != nil {
		return "", fmt.Errorf("compose client: %w", err)
	}
	// Stream compose build/up progress to the same destination as docker builds.
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
	// TS openDockerComposeDevContainer only starts the project when the service
	// container is missing or not running.
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

// runLifecycleForUp runs lifecycle hooks on a newly created container. It returns
// a non-nil error (a *coreerrors.ContainerError with a TS-compatible description)
// only when a lifecycle hook fails; probe/dotfiles issues stay non-fatal warnings.
func (r *upRunner) runLifecycleForUp(ctx context.Context, containerID string, cfg *config.DevContainer, workDir string, remoteUser string) error {
	logger, engine, opts := r.log, r.engine, r.opts
	// Build merged remote env: probed shell env → CLI --remote-env → config remoteEnv
	probeStrategy := lifecycle.UserEnvProbeStrategy(opts.defaultUserEnvProbe)
	if cfg.UserEnvProbe != "" {
		probeStrategy = lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}

	var mergedRemoteEnv []string

	// Probe user environment inside the container (with caching)
	for k, v := range lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, remoteUser, probeStrategy, opts.containerSessionDataFolder) {
		mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Secrets override probed env and are visible to lifecycle hooks.
	if opts.secretsFile != "" {
		if secretEnvs, err := readSecretsFile(opts.secretsFile); err == nil {
			mergedRemoteEnv = append(mergedRemoteEnv, secretEnvs...)
		} else {
			logger.Write(fmt.Sprintf("Warning: secrets file: %v", err), log.LevelWarning)
		}
	}

	// CLI --remote-env overrides probed/secrets
	mergedRemoteEnv = append(mergedRemoteEnv, opts.remoteEnvs...)
	// Config remoteEnv overrides everything
	for k, v := range cfg.RemoteEnv {
		if v != nil {
			mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, *v))
		}
	}

	shellServer := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, mergedRemoteEnv...)
	defer shellServer.Close()

	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger, WorkDir: workDir}

	// Read metadata from the image (features' lifecycle commands are baked into
	// the image), NOT the container: the container now carries the config metadata
	// as a label, so reading it and then re-adding the config commands below would
	// run every hook twice.
	inspectResp, inspErr := engine.InspectContainer(ctx, containerID)
	var merged *imagemeta.MergedConfig
	if inspErr == nil && inspectResp.Image != "" {
		if img, imgErr := engine.InspectImage(ctx, inspectResp.Image); imgErr == nil && img.Config != nil {
			merged = imagemeta.MergeConfiguration(imagemeta.ReadMetadataFromLabels(img.Config.Labels, logger))
		}
	}
	if merged == nil {
		merged = imagemeta.MergeConfiguration(nil)
	}

	// Add commands from config. Preserve the raw value so object-form commands
	// continue to run in parallel like the TS CLI.
	if !cfg.OnCreateCommand.IsEmpty() {
		merged.OnCreateCommands = append(merged.OnCreateCommands, cfg.OnCreateCommand.Raw())
	}
	if !cfg.UpdateContentCommand.IsEmpty() {
		merged.UpdateContentCommands = append(merged.UpdateContentCommands, cfg.UpdateContentCommand.Raw())
	}
	if !cfg.PostCreateCommand.IsEmpty() {
		merged.PostCreateCommands = append(merged.PostCreateCommands, cfg.PostCreateCommand.Raw())
	}
	if !cfg.PostStartCommand.IsEmpty() {
		merged.PostStartCommands = append(merged.PostStartCommands, cfg.PostStartCommand.Raw())
	}
	if !cfg.PostAttachCommand.IsEmpty() {
		merged.PostAttachCommands = append(merged.PostAttachCommands, cfg.PostAttachCommand.Raw())
	}

	applyLifecycleMarkers(shellServer, merged, inspectResp, opts.prebuild)

	hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
		SkipNonBlocking: opts.skipNonBlocking,
		SkipPostAttach:  opts.skipPostAttach,
		Prebuild:        opts.prebuild,
		// Dotfiles install after postCreateCommand (before postStart), as remoteUser
		// (executor runs docker exec -u) and with secrets in the env, matching TS.
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

// hookContainerError is TS runLifecycleCommand's ContainerError for a failed
// lifecycle command: the description names the command and where it comes from,
// the message is the command's own error, and up reports the container. Any
// other error gets the command's generic description (fallback).
func hookContainerError(hookErr error, containerID, fallback string) *coreerrors.ContainerError {
	he, ok := errors.AsType[*lifecycle.HookError](hookErr)
	if !ok {
		return &coreerrors.ContainerError{Description: fallback, OriginalError: hookErr, ContainerID: containerID}
	}
	return &coreerrors.ContainerError{Description: he.Description(), OriginalError: he.Err, ContainerID: containerID}
}

// applyLifecycleMarkers drops the hooks a previous run already ran, as TS
// runPostCreateCommand/runPostStartCommand do with updateMarkerFile for up,
// set-up and run-user-commands alike: onCreate, updateContent and postCreate run
// once per container (createdAt), postStart once per start (startedAt), and
// postAttach always. The marker is written before the hook runs, so a hook that
// failed is not run again. --prebuild reruns updateContentCommand.
func applyLifecycleMarkers(server *lifecycle.ShellServer, merged *imagemeta.MergedConfig, inspect container.InspectResponse, prebuild bool) {
	folder := resolveContainerUserDataFolder(server)
	if folder == "" {
		return
	}
	startedAt := ""
	if inspect.State != nil {
		startedAt = inspect.State.StartedAt
	}
	if !markerShouldRun(server, folder, "onCreateCommand", inspect.Created) {
		merged.OnCreateCommands = nil
	}
	if !markerShouldRun(server, folder, "updateContentCommand", inspect.Created) && !prebuild {
		merged.UpdateContentCommands = nil
	}
	if !markerShouldRun(server, folder, "postCreateCommand", inspect.Created) {
		merged.PostCreateCommands = nil
	}
	if !markerShouldRun(server, folder, "postStartCommand", startedAt) {
		merged.PostStartCommands = nil
	}
}

// dotfilesAfterPostCreate installs the dotfiles repository where TS
// runLifecycleHooks does: after postCreateCommand, before postStartCommand. A
// failure is logged and not fatal.
func dotfilesAfterPostCreate(logger log.Logger, executor lifecycle.CommandExecutor, cfg lifecycle.DotfilesConfig) func() {
	return func() {
		if cfg.Repository == "" {
			return
		}
		if err := lifecycle.InstallDotfiles(logger, executor, cfg); err != nil {
			logger.Write(fmt.Sprintf("Dotfiles installation failed: %v", err), log.LevelWarning)
		}
	}
}

// resolveContainerUserDataFolder returns the in-container folder where lifecycle
// marker files live: <HOME>/.devcontainer (matching TS getUserDataFolder with the
// default containerDataFolder). Returns "" if HOME cannot be resolved.
func resolveContainerUserDataFolder(server *lifecycle.ShellServer) string {
	home, code, err := server.Exec(`printf %s "$HOME"`)
	home = strings.TrimSpace(home)
	if err != nil || code != 0 || home == "" {
		return ""
	}
	return home + "/.devcontainer"
}

// markerShouldRun reports whether a lifecycle hook should run, updating its marker
// file as a side effect. It runs (and rewrites the marker) only when the marker's
// content differs from the container's createdAt/startedAt timestamp — matching
// the TS updateMarkerFile — so a repeated `up` on the same container skips hooks
// that already ran. An empty timestamp (unknown) defaults to running.
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

// featureBuildOptions are the Feature install options of up's image builds;
// platform is the target platform ("" for the daemon's).
func (r *upRunner) featureBuildOptions(cfg *config.DevContainer, loadResult *config.LoadResult, platform string) *FeatureBuildOptions {
	opts := r.opts
	return &FeatureBuildOptions{
		Platform:                    platform,
		Secrets:                     buildSecretsFromFile(opts.secretsFile),
		OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
		FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
		SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
		Lockfile:                    opts.experimentalLockfile,
		FrozenLockfile:              opts.experimentalFrozenLockfile,
		NoLockfile:                  opts.noLockfile,
		ConfigPath:                  cfg.ConfigFilePath,
		LockfileExcludeIDs:          opts.lockfileExcludeIDs,
		HostSub:                     &loadResult.HostSub,
	}
}
