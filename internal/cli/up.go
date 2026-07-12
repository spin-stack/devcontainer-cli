package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	dockermount "github.com/moby/moby/api/types/mount"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
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
	workspaceMountConsistency   string
	updateRemoteUserUIDDefault  string
	gpuAvailability             string
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
	for _, v := range []struct {
		flag, val string
		choices   []string
	}{
		{"log-level", opts.logLevel, []string{"info", "debug", "trace"}},
		{"log-format", opts.logFormat, []string{"text", "json"}},
		{"buildkit", opts.buildkit, []string{"auto", "never"}},
		{"workspace-mount-consistency", opts.workspaceMountConsistency, []string{"consistent", "cached", "delegated"}},
		{"gpu-availability", opts.gpuAvailability, []string{"all", "detect", "none"}},
		{"default-user-env-probe", opts.defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
		{"update-remote-user-uid-default", opts.updateRemoteUserUIDDefault, []string{"on", "off", "never"}},
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return err
		}
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
		Version:    cliVersion(),
		Level:      log.ParseLevel(opts.logLevel),
		Format:     opts.logFormat,
		Writer:     logDst,
		Dimensions: logDimensions(opts.terminalColumns, opts.terminalRows),
		Secrets:    secretValuesFromFile(opts.secretsFile),
	})

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

	// Look for existing container early — needed for --expect-existing-container
	// and for the --id-label-only path (no workspace-folder).
	if len(idLabels) > 0 {
		existingIDs, _ := engine.ListContainers(ctx, true, idLabels)

		if len(existingIDs) > 0 && opts.removeExisting {
			logger.Write(fmt.Sprintf("Removing existing container %s...", existingIDs[0]), log.LevelInfo)
			_ = engine.RemoveContainer(ctx, existingIDs[0])
			existingIDs = nil
		}

		if len(existingIDs) > 0 {
			containerID := existingIDs[0]
			logger.Write(fmt.Sprintf("Using existing container %s", containerID), log.LevelInfo)

			inspectResp, inspErr := engine.InspectContainer(ctx, containerID)
			if inspErr == nil && inspectResp.State != nil && !inspectResp.State.Running {
				logger.Write("Starting existing container...", log.LevelInfo)
				if startErr := engine.StartContainer(ctx, containerID); startErr != nil {
					return writeErrorResult(out, fmt.Sprintf("Failed to start container: %v", startErr))
				}
			}

			// Run lifecycle hooks on reattach: postAttach every time,
			// postStart when restarted; onCreate/updateContent/postCreate are gated
			// by markers. Hooks come from the container's baked metadata.
			if !opts.skipPostCreate {
				run.runReattachLifecycle(ctx, containerID)
			}

			return run.finishUp(ctx, containerID, nil, nil)
		}

		if opts.expectExistingContainer {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: "The expected container does not exist.",
			}))
		}

		// No existing container found with --id-label, fall through to config-based creation.
		if workspaceFolder == "" {
			return writeErrorResult(out, "No dev container config and no workspace found.")
		}
	}

	// Load config — requires workspaceFolder (guaranteed non-empty at this point)
	loadResult, err := config.LoadDevContainerConfig(workspaceFolder, configPath, overridePath)
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	cfg := loadResult.Config

	// Merge --additional-features into config (config features have priority)
	var mergeErr error
	opts.lockfileExcludeIDs, mergeErr = mergeAdditionalFeatures(cfg, opts.additionalFeatures)
	if mergeErr != nil {
		return writeErrorResult(out, mergeErr.Error())
	}

	if derr := enforceDisallowedFeatures(ctx, cfg, logger); derr != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(derr))
	}

	// Run initializeCommand on the host (before container creation).
	// A failure aborts `up` with an error outcome, matching the TS CLI (which
	// throws a ContainerError). Swallowing it produced silently-broken setups.
	if !cfg.InitializeCommand.IsEmpty() {
		if err := lifecycle.RunInitializeCommand(ctx, logger, &cfg.InitializeCommand, workspaceFolder); err != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: "The initializeCommand in the devcontainer.json failed.",
			}))
		}
	}

	dockerClient := docker.NewClient(opts.dockerPath, nil, logger)
	run.docker = dockerClient

	// Detect BuildKit
	var useBuildx bool
	if opts.buildkit != "never" {
		bk := dockerClient.DetectBuildKit(ctx)
		useBuildx = bk.Available
	}

	// Derive id labels from workspace if not provided via --id-label
	if len(idLabels) == 0 {
		idLabels = []string{
			fmt.Sprintf("devcontainer.local_folder=%s", workspaceFolder),
		}
		if loadResult.Config.ConfigFilePath != "" {
			idLabels = append(idLabels, fmt.Sprintf("devcontainer.config_file=%s", loadResult.Config.ConfigFilePath))
		}
	}

	// Check for existing container
	existingIDs, _ := engine.ListContainers(ctx, true, idLabels)

	if len(existingIDs) > 0 && opts.removeExisting {
		logger.Write(fmt.Sprintf("Removing existing container %s...", existingIDs[0]), log.LevelInfo)
		_ = engine.RemoveContainer(ctx, existingIDs[0])
		existingIDs = nil
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

	result := map[string]interface{}{
		"outcome":               "success",
		"containerId":           containerID,
		"remoteUser":            remoteUser,
		"remoteWorkspaceFolder": remoteWorkspaceFolder,
	}
	if opts.composeProjectName != "" {
		result["composeProjectName"] = opts.composeProjectName
	}

	if opts.includeConfig || opts.includeMergedConfig {
		// Reuse inspectResp from above — no need to re-inspect the same container.
		containerEnv := map[string]string{}
		if inspectResp.Config != nil {
			for _, e := range inspectResp.Config.Env {
				if i := strings.IndexByte(e, '='); i >= 0 {
					containerEnv[e[:i]] = e[i+1:]
				}
			}
		}

		if opts.includeConfig {
			cfgJSON, _ := json.Marshal(cfg)
			var cfgGeneric interface{}
			json.Unmarshal(cfgJSON, &cfgGeneric)
			substituted := config.SubstituteContainer("linux", containerEnv, cfgGeneric)
			if subMap, ok := substituted.(map[string]interface{}); ok {
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
					subMap["configFilePath"] = map[string]interface{}{
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
			if cfgResult, ok := result["configuration"].(map[string]interface{}); ok {
				if cfpURI, ok := cfgResult["configFilePath"]; ok {
					merged.ConfigFilePath = cfpURI
				}
			}

			// Substitute containerEnv into merged config
			mergedJSON, _ := json.Marshal(merged)
			var mergedGeneric interface{}
			json.Unmarshal(mergedJSON, &mergedGeneric)
			mergedSub := config.SubstituteContainer("linux", containerEnv, mergedGeneric)
			// Carry over config-only properties spread into mergedConfiguration by
			// the TS CLI that the typed MergedConfig does not model.
			if mm, ok := mergedSub.(map[string]interface{}); ok {
				if cfgResult, ok := result["configuration"].(map[string]interface{}); ok {
					for _, k := range mergedPassthroughKeys {
						if v, ok := cfgResult[k]; ok {
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

// finishUp produces the JSON result for an existing container found via --id-label
// (no config/loadResult available). cfg and loadResult may be nil.
func (r *upRunner) finishUp(ctx context.Context, containerID string, cfg *config.DevContainer, loadResult *config.LoadResult) error {
	out, logger, engine := r.out, r.log, r.engine
	inspectResp, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Failed to inspect container: %v", err))
	}

	remoteUser := ""
	if cfg != nil {
		remoteUser = cfg.RemoteUser
	}
	if remoteUser == "" && inspectResp.Config != nil {
		metaEntries := imagemeta.ReadMetadataFromLabels(inspectResp.Config.Labels, logger)
		if len(metaEntries) > 0 {
			metaMerged := imagemeta.MergeConfiguration(metaEntries)
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

	remoteWorkspaceFolder := ""
	if loadResult != nil {
		remoteWorkspaceFolder = loadResult.WorkspaceConfig.WorkspaceFolder
	}

	result := map[string]interface{}{
		"outcome":               "success",
		"containerId":           containerID,
		"remoteUser":            remoteUser,
		"remoteWorkspaceFolder": remoteWorkspaceFolder,
	}

	return writeSuccessJSON(out, result)
}

// runReattachLifecycle runs lifecycle hooks when reattaching to an existing
// container (found via --id-label, no config loaded). Hooks come from the
// container's baked devcontainer.metadata; markers gate onCreate/updateContent/
// postCreate (already ran) and postStart (per startedAt), while postAttach runs
// every attach — matching the TS reattach path. Best-effort: probe
// or shell failures degrade to a warning.
func (r *upRunner) runReattachLifecycle(ctx context.Context, containerID string) {
	logger, engine, opts := r.log, r.engine, r.opts
	inspectResp, err := engine.InspectContainer(ctx, containerID)
	if err != nil || inspectResp.Config == nil {
		return
	}
	entries := imagemeta.ReadMetadataFromLabels(inspectResp.Config.Labels, logger)
	merged := imagemeta.MergeConfiguration(entries)
	if len(merged.OnCreateCommands)+len(merged.UpdateContentCommands)+len(merged.PostCreateCommands)+len(merged.PostStartCommands)+len(merged.PostAttachCommands) == 0 {
		return
	}

	remoteUser := merged.RemoteUser
	if remoteUser == "" {
		remoteUser = inspectResp.Config.User
	}

	// Probe remote env + secrets. The config's remoteEnv is already baked into the
	// container metadata (merged.RemoteEnv), so it is not re-added here.
	var mergedRemoteEnv []string
	probeStrategy := lifecycle.UserEnvProbeStrategy(opts.defaultUserEnvProbe)
	if merged.UserEnvProbe != "" {
		probeStrategy = lifecycle.UserEnvProbeStrategy(merged.UserEnvProbe)
	}
	if probeStrategy != lifecycle.ProbeNone {
		if probeServer, probeErr := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, logger); probeErr == nil {
			probedEnv, _ := lifecycle.ProbeRemoteEnv(logger, probeServer, probeStrategy, remoteUser, opts.containerSessionDataFolder)
			probeServer.Close()
			for k, v := range probedEnv {
				mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}
	if opts.secretsFile != "" {
		if secretEnvs, sErr := readSecretsFile(opts.secretsFile); sErr == nil {
			mergedRemoteEnv = append(mergedRemoteEnv, secretEnvs...)
		}
	}
	mergedRemoteEnv = append(mergedRemoteEnv, opts.remoteEnvs...)
	for k, v := range merged.RemoteEnv {
		if v != nil {
			mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, *v))
		}
	}

	shellServer, err := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, logger, mergedRemoteEnv...)
	if err != nil {
		logger.Write(fmt.Sprintf("Could not start shell server for reattach hooks: %v", err), log.LevelWarning)
		return
	}
	defer shellServer.Close()
	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger}

	if markerFolder := resolveContainerUserDataFolder(shellServer); markerFolder != "" {
		createdAt := inspectResp.Created
		startedAt := ""
		if inspectResp.State != nil {
			startedAt = inspectResp.State.StartedAt
		}
		if !markerShouldRun(shellServer, markerFolder, "onCreateCommand", createdAt) {
			merged.OnCreateCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "updateContentCommand", createdAt) {
			merged.UpdateContentCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "postCreateCommand", createdAt) {
			merged.PostCreateCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "postStartCommand", startedAt) {
			merged.PostStartCommands = nil
		}
	}

	if hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
		SkipNonBlocking: opts.skipNonBlocking,
		SkipPostAttach:  opts.skipPostAttach,
		Prebuild:        opts.prebuild,
	}); hookErr != nil {
		logger.Write(fmt.Sprintf("Lifecycle hooks failed: %v", hookErr), log.LevelWarning)
	}
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
	if baseInspect, inspErr := engine.InspectImage(ctx, baseImage); inspErr == nil && baseInspect.Config != nil {
		metadata = append(metadata, imagemeta.ReadMetadataFromLabels(baseInspect.Config.Labels, logger)...)
	}
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

	logger.Write(string(buildResult.Stderr), log.LevelInfo)

	// Extend with features if any
	if len(cfg.Features) > 0 {
		names, err := extendImageWithFeatures(ctx, logger, dockerClient, engine, imageName, cfg.Features, useBuildx, nil, &FeatureBuildOptions{
			OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
			FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
			SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
			Lockfile:                    opts.experimentalLockfile,
			FrozenLockfile:              opts.experimentalFrozenLockfile,
			ConfigPath:                  cfg.ConfigFilePath,
			LockfileExcludeIDs:          opts.lockfileExcludeIDs,
		})
		if err != nil {
			return "", fmt.Errorf("install features: %w", err)
		}
		if len(names) > 0 {
			imageName = names[0]
		}
	}

	imageName = r.maybeUpdateRemoteUserUID(ctx, imageName, cfg.RemoteUser, cfg)

	return r.runContainer(ctx, imageName, cfg, loadResult, workspaceFolder, idLabels)
}

func (r *upRunner) fromImage(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string, useBuildx bool) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	if cfg.Image == "" {
		return "", fmt.Errorf("no image specified in devcontainer.json")
	}

	// Pull if needed
	_, err := engine.InspectImage(ctx, cfg.Image)
	if err != nil {
		logger.Write(fmt.Sprintf("Pulling image %s...", cfg.Image), log.LevelInfo)
		if pullErr := engine.PullImage(ctx, cfg.Image); pullErr != nil {
			return "", fmt.Errorf("pull image %q: %w", cfg.Image, pullErr)
		}
	}

	// Extend with features if any
	imageName := cfg.Image
	if len(cfg.Features) > 0 {
		names, err := extendImageWithFeatures(ctx, logger, dockerClient, engine, cfg.Image, cfg.Features, useBuildx, nil, &FeatureBuildOptions{
			OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
			FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
			SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
			Lockfile:                    opts.experimentalLockfile,
			FrozenLockfile:              opts.experimentalFrozenLockfile,
			ConfigPath:                  cfg.ConfigFilePath,
			LockfileExcludeIDs:          opts.lockfileExcludeIDs,
		})
		if err != nil {
			return "", fmt.Errorf("install features: %w", err)
		}
		if len(names) > 0 {
			imageName = names[0]
		}
	}

	imageName = r.maybeUpdateRemoteUserUID(ctx, imageName, cfg.RemoteUser, cfg)

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

	image = r.maybeUpdateRemoteUserUID(ctx, image, cfg.RemoteUser, cfg)
	return r.runContainer(ctx, image, cfg, loadResult, workspaceFolder, idLabels)
}

// maybeUpdateRemoteUserUID rebuilds imageName with the remote user's UID/GID
// remapped to the host user's, matching the TS updateRemoteUserUID. Returns the
// (possibly new) image name; a no-op when disabled, when remoteUser is empty or
// root, or when the host UID is not a regular user. Applies to both the image
// and Dockerfile paths of up (previously only the image path called it, so
// Dockerfile-based configs with a remoteUser had broken bind-mount permissions
// on Linux).
func (r *upRunner) maybeUpdateRemoteUserUID(ctx context.Context, imageName, remoteUser string, cfg *config.DevContainer) string {
	logger, dockerClient, opts := r.log, r.docker, r.opts
	shouldUpdateUID := opts.updateRemoteUserUIDDefault == "on"
	if cfg.UpdateRemoteUserUID != nil {
		shouldUpdateUID = *cfg.UpdateRemoteUserUID
	}
	if !shouldUpdateUID || remoteUser == "" || remoteUser == "root" {
		return imageName
	}
	hostUID := os.Getuid()
	hostGID := os.Getgid()
	if hostUID <= 0 {
		return imageName
	}
	bkInfo := dockerClient.DetectBuildKit(ctx)
	updatedImage, err := docker.UpdateRemoteUserUID(ctx, dockerClient, logger, imageName, remoteUser, hostUID, hostGID, bkInfo.Available)
	if err != nil {
		logger.Write(fmt.Sprintf("UID update failed: %v", err), log.LevelWarning)
		return imageName
	}
	return updatedImage
}

func (r *upRunner) runContainer(ctx context.Context, imageName string, cfg *config.DevContainer, loadResult *config.LoadResult, workspaceFolder string, idLabels []string) (string, error) {
	logger, dockerClient, engine, opts := r.log, r.docker, r.engine, r.opts
	// Inspect image once — used for metadata and containerUser resolution.
	imageInspect, _ := engine.InspectImage(ctx, imageName)

	var metaEntries []imagemeta.Entry
	if imageInspect.Config != nil {
		metaEntries = append(metaEntries, imagemeta.ReadMetadataFromLabels(imageInspect.Config.Labels, logger)...)
	}
	metaEntries = append(metaEntries, configToMetadataEntry(cfg, opts.omitConfigRemoteEnvFromMeta))
	merged := imagemeta.MergeConfiguration(metaEntries)

	// The container carries the full devcontainer.metadata (image + config) so the
	// CLI/VS Code can read remoteUser/lifecycle/customizations back on reattach —
	// image-based containers previously had no metadata label. The label matches
	// what the TS CLI adds with `-l`.
	containerMetadataLabel := imagemeta.GenerateMetadataLabel(metaEntries)

	containerWorkspace := loadResult.WorkspaceConfig.WorkspaceFolder

	// Build CMD script with feature entrypoints
	var cmdScript strings.Builder
	cmdScript.WriteString("echo Container started\ntrap \"exit 0\" 15\n")
	for _, ep := range merged.Entrypoints {
		cmdScript.WriteString(ep + "\n")
	}
	cmdScript.WriteString("exec \"$@\"\nwhile sleep 1 & wait $!; do :; done")

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
	var wsMount dockermount.Mount
	if spec := loadResult.WorkspaceConfig.WorkspaceMount; spec != "" {
		parsed, parseErr := docker.ParseMountSpec(spec)
		if parseErr != nil {
			return "", fmt.Errorf("invalid workspaceMount %q: %w", spec, parseErr)
		}
		wsMount = parsed
	} else {
		wsMount = dockermount.Mount{Type: dockermount.TypeBind, Source: workspaceFolder, Target: containerWorkspace}
	}
	if opts.workspaceMountConsistency != "" {
		wsMount.Consistency = dockermount.Consistency(opts.workspaceMountConsistency)
	}
	mounts := []dockermount.Mount{wsMount}
	idLabelMap := map[string]string{}
	for _, label := range idLabels {
		if i := strings.IndexByte(label, '='); i >= 0 {
			idLabelMap[label[:i]] = label[i+1:]
		}
	}
	devcontainerID := config.ComputeDevContainerID(idLabelMap)
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
	cmdArgs := []string{"-c", cmdScript.String(), "-"}
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
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "COMPOSE_PROJECT_NAME=") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "COMPOSE_PROJECT_NAME="))
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
			for i := len(composeFiles) - 1; i >= 0; i-- {
				raw, readErr := os.ReadFile(composeFiles[i])
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

// overrideDirFor returns the directory that compose override files are written
// to. When userDataFolder is set the overrides live in a stable subdir so a
// container can be restarted without re-injecting them; otherwise a temp dir is
// used and the returned cleanup removes it. cleanup is always non-nil.
func overrideDirFor(userDataFolder, tmpPattern string) (dir string, cleanup func(), err error) {
	if userDataFolder != "" {
		dir = filepath.Join(userDataFolder, "docker-compose")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", func() {}, fmt.Errorf("create override dir: %w", err)
		}
		return dir, func() {}, nil
	}
	dir, err = os.MkdirTemp("", tmpPattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp override dir: %w", err)
	}
	return dir, func() { os.RemoveAll(dir) }, nil
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

func parseComposeServiceBuild(composeConfig map[string]interface{}, service string) composeServiceBuild {
	var out composeServiceBuild
	services, ok := composeConfig["services"].(map[string]interface{})
	if !ok {
		return out
	}
	svc, ok := services[service].(map[string]interface{})
	if !ok {
		return out
	}
	if img, ok := svc["image"].(string); ok {
		out.Image = img
	}
	build, ok := svc["build"].(map[string]interface{})
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
	if args, ok := build["args"].(map[string]interface{}); ok {
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

	projectName := resolveComposeProjectName(ctx, cfg, env, composeFiles, composeClient)
	logger.Write(fmt.Sprintf("Compose project: %s, service: %s", projectName, cfg.Service), log.LevelInfo)

	// Check for existing compose container (skip build if it exists)
	existingComposeIDs, _ := engine.ListContainers(ctx, true, []string{
		fmt.Sprintf("com.docker.compose.project=%s", projectName),
		fmt.Sprintf("com.docker.compose.service=%s", cfg.Service),
	})

	if opts.expectExistingContainer && len(existingComposeIDs) == 0 {
		return "", fmt.Errorf("the expected container does not exist")
	}

	noComposeBuild := len(existingComposeIDs) > 0
	featureImageName := ""
	if len(cfg.Features) > 0 {
		// Read compose config to determine service setup
		composeConfig, configErr := composeClient.Config(ctx, composeFiles, "")
		var svcBuild composeServiceBuild
		if configErr == nil {
			svcBuild = parseComposeServiceBuild(composeConfig, cfg.Service)
		}
		serviceHasBuild := svcBuild.HasBuild
		serviceImage := svcBuild.Image
		serviceDockerfile := svcBuild.Dockerfile
		serviceBuildContext := svcBuild.Context
		serviceBuildTarget := svcBuild.Target

		// Fetch and resolve features
		fetchResult, fetchErr := fetchFeatureSets(logger, nil, cfg.Features, filepath.Dir(cfg.ConfigFilePath), opts.skipFeatureAutoMapping, nil)
		if fetchErr != nil {
			return "", fetchErr
		}

		featureSets := []*features.Set{}
		tmpDir := ""
		if fetchResult != nil {
			featureSets = fetchResult.FeatureSets
			tmpDir = fetchResult.TmpDir
			defer os.RemoveAll(tmpDir)
		}

		if serviceHasBuild && serviceDockerfile != "" {
			// TS CLI approach: inject features into the Dockerfile
			// Read the original Dockerfile
			configDir := filepath.Dir(cfg.ConfigFilePath)
			originalDockerfilePath := serviceDockerfile
			if !filepath.IsAbs(originalDockerfilePath) {
				if serviceBuildContext != "" {
					originalDockerfilePath = filepath.Join(serviceBuildContext, originalDockerfilePath)
				}
				if !filepath.IsAbs(originalDockerfilePath) {
					originalDockerfilePath = filepath.Join(configDir, originalDockerfilePath)
				}
			}

			dockerfileContent, readErr := os.ReadFile(originalDockerfilePath)
			if readErr != nil {
				return "", fmt.Errorf("read Dockerfile for compose service: %w", readErr)
			}

			// Find base image for metadata — pass compose build args for variable resolution
			df := docker.ExtractDockerfile(string(dockerfileContent))
			baseImage := docker.FindBaseImage(df, svcBuild.Args, serviceBuildTarget)

			// Inspect base image once for metadata and user resolution.
			baseImageInspect, _ := engine.InspectImage(ctx, baseImage)

			metadata := []imagemeta.Entry{}
			var baseEntries []imagemeta.Entry
			if baseImageInspect.Config != nil {
				baseEntries = imagemeta.ReadMetadataFromLabels(baseImageInspect.Config.Labels, logger)
				metadata = append(metadata, baseEntries...)
			}
			for _, fs := range featureSets {
				if len(fs.Features) > 0 {
					feat := fs.Features[0]
					metadata = append(metadata, imagemeta.Entry{
						ID: feat.ID, Init: feat.Init, Privileged: feat.Privileged,
						CapAdd: feat.CapAdd, SecurityOpt: feat.SecurityOpt,
						ContainerEnv: feat.ContainerEnv, Mounts: feat.Mounts,
						Customizations: feat.Customizations, Entrypoint: feat.Entrypoint,
					})
				}
			}
			metadata = append(metadata, configToMetadataEntry(cfg, opts.omitConfigRemoteEnvFromMeta))

			containerUser := "root"
			remoteUser := "root"
			if baseImageInspect.Config != nil {
				if baseImageInspect.Config.User != "" {
					containerUser = baseImageInspect.Config.User
				}
				if len(baseEntries) > 0 {
					baseMerged := imagemeta.MergeConfiguration(baseEntries)
					if baseMerged.ContainerUser != "" {
						containerUser = baseMerged.ContainerUser
					}
					if baseMerged.RemoteUser != "" {
						remoteUser = baseMerged.RemoteUser
					}
				}
			}
			if remoteUser == "root" {
				remoteUser = containerUser
			}

			// When a build target is set, features must extend that stage.
			// Otherwise, add a synthetic name to the final stage so the injected
			// Dockerfile can reference it safely.
			baseStageName := serviceBuildTarget
			if baseStageName == "" {
				baseStageName = "dev_containers_auto_added_stage_label"
				_, modifiedDF := docker.EnsureFinalStageName(string(dockerfileContent), baseStageName)
				if modifiedDF != "" {
					dockerfileContent = []byte(modifiedDF)
				}
			}

			// Generate feature Dockerfile content for compose injection
			featureContent := imagemeta.GenerateExtendImageBuildForCompose(
				baseStageName, featureSets, metadata, containerUser, remoteUser, nil,
			)
			combinedDockerfile := string(dockerfileContent) + "\n" + featureContent

			// Copy feature content to compose build context so COPY works
			buildCtx := serviceBuildContext
			if !filepath.IsAbs(buildCtx) {
				buildCtx = filepath.Join(filepath.Dir(cfg.ConfigFilePath), buildCtx)
			}
			for i := range featureSets {
				srcDir := filepath.Join(tmpDir, fmt.Sprintf("_dev_container_feature_%d", i))
				dstDir := filepath.Join(buildCtx, fmt.Sprintf("_dev_container_feature_%d", i))
				os.RemoveAll(dstDir)
				if cpErr := copyDir(srcDir, dstDir); cpErr != nil {
					return "", fmt.Errorf("copy feature %d to build context: %w", i, cpErr)
				}
				defer os.RemoveAll(dstDir)
			}

			// Write combined Dockerfile to the build context
			combinedPath := filepath.Join(buildCtx, "Dockerfile-with-features")
			if err := os.WriteFile(combinedPath, []byte(combinedDockerfile), 0644); err != nil {
				return "", fmt.Errorf("write combined Dockerfile: %w", err)
			}
			defer os.Remove(combinedPath)

			// Persist the build override when possible so a stopped compose container
			// can be restarted without re-injecting features or losing the override.
			overrideDir, cleanup, err := overrideDirFor(opts.userDataFolder, "devcontainer-compose-build-")
			if err != nil {
				return "", err
			}
			defer cleanup()

			buildOverride := docker.NewComposeOverride()
			svc := buildOverride.Service(cfg.Service)
			svc.Build = &docker.BuildOverride{
				Dockerfile: combinedPath,
				Target:     "dev_containers_target_stage",
			}
			if !serviceHasBuild || serviceBuildContext == "" {
				svc.Build.Context = configDir
			}

			buildOverridePath := filepath.Join(overrideDir, fmt.Sprintf("docker-compose.devcontainer.build-%s.json", projectName))
			if wErr := buildOverride.WriteFile(buildOverridePath); wErr != nil {
				return "", fmt.Errorf("write compose build override: %w", wErr)
			}
			composeFiles = append(composeFiles, buildOverridePath)

			logger.Write("Injected features into compose Dockerfile", log.LevelInfo)
		} else {
			// Image-only service: build features as separate image
			if !noComposeBuild {
				buildErr := composeClient.Build(ctx, composeFiles, "", nil, []string{cfg.Service}, opts.buildNoCache)
				if buildErr != nil {
					return "", fmt.Errorf("compose build: %w", buildErr)
				}

				baseImageName := serviceImage
				if baseImageName == "" {
					baseImageName = fmt.Sprintf("%s-%s", projectName, cfg.Service)
				}

				logger.Write(fmt.Sprintf("Installing features on compose service %s...", cfg.Service), log.LevelInfo)
				names, extErr := extendImageWithFeatures(ctx, logger, dockerClient, engine, baseImageName, cfg.Features, useBuildx, nil, &FeatureBuildOptions{
					OverrideFeatureInstallOrder: cfg.OverrideFeatureInstallOrder,
					FeaturesBasePath:            filepath.Dir(cfg.ConfigFilePath),
					SkipFeatureAutoMapping:      opts.skipFeatureAutoMapping,
					Lockfile:                    opts.experimentalLockfile,
					FrozenLockfile:              opts.experimentalFrozenLockfile,
					ConfigPath:                  cfg.ConfigFilePath,
					LockfileExcludeIDs:          opts.lockfileExcludeIDs,
				})
				if extErr != nil {
					return "", fmt.Errorf("install features on compose service: %w", extErr)
				}
				if len(names) > 0 {
					featureImageName = names[0]
				}
			}
		}
	}

	// Generate containerEnv override if containerEnv is set
	if len(cfg.ContainerEnv) > 0 {
		envOverrideDir, cleanup, err := overrideDirFor(opts.userDataFolder, "devcontainer-compose-env-")
		if err != nil {
			return "", err
		}
		defer cleanup()

		envOverride := docker.NewComposeOverride()
		envSvc := envOverride.Service(cfg.Service)
		for k, v := range cfg.ContainerEnv {
			envSvc.AddEnv(k, v) // escaping handled by ComposeOverride
		}
		envOverridePath := filepath.Join(envOverrideDir, fmt.Sprintf("docker-compose.devcontainer.env-%s.json", projectName))
		if wErr := envOverride.WriteFile(envOverridePath); wErr != nil {
			return "", fmt.Errorf("write compose env override: %w", wErr)
		}
		composeFiles = append(composeFiles, envOverridePath)
	}

	if len(cfg.Mounts) > 0 {
		mountOverrideDir, cleanup, err := overrideDirFor(opts.userDataFolder, "devcontainer-compose-mounts-")
		if err != nil {
			return "", err
		}
		defer cleanup()

		idLabelMap := map[string]string{}
		for _, label := range idLabels {
			if i := strings.IndexByte(label, '='); i >= 0 {
				idLabelMap[label[:i]] = label[i+1:]
			}
		}
		devcontainerID := config.ComputeDevContainerID(idLabelMap)
		volumeSpecs, namedVolumes, err := composeVolumeSpecsFromMetadata(metadataMounts(cfg), devcontainerID)
		if err != nil {
			return "", fmt.Errorf("resolve compose mounts: %w", err)
		}

		mountOverride := docker.NewComposeOverride()
		mountSvc := mountOverride.Service(cfg.Service)
		for _, spec := range volumeSpecs {
			mountSvc.AddVolume(spec)
		}
		for _, volumeName := range namedVolumes {
			mountOverride.AddVolume(volumeName)
		}

		mountOverridePath := filepath.Join(mountOverrideDir, fmt.Sprintf("docker-compose.devcontainer.mounts-%s.json", projectName))
		if wErr := mountOverride.WriteFile(mountOverridePath); wErr != nil {
			return "", fmt.Errorf("write compose mounts override: %w", wErr)
		}
		composeFiles = append(composeFiles, mountOverridePath)
	}

	// Always create a compose override for the target service to attach the
	// devcontainer.* labels (id-labels + metadata) so the container is
	// discoverable by exec and VS Code — matching the TS CLI, which sets
	// additionalLabels on the compose service. When features were installed
	// we also add the runtime settings (image, entrypoint, env, mounts).
	{
		overrideDir, cleanup, err := overrideDirFor(opts.userDataFolder, "devcontainer-compose-features-")
		if err != nil {
			return "", err
		}
		defer cleanup()

		override := docker.NewComposeOverride()
		svc := override.Service(cfg.Service)

		withFeatures := featureImageName != "" || len(cfg.Features) > 0
		if featureImageName != "" {
			svc.Image = featureImageName
		}

		// Read metadata from the feature image (or the service's base image) for
		// both the runtime settings and the devcontainer.metadata label. Resolve
		// the service's declared image via `docker compose config` rather than
		// guessing "<project>-<service>": with a generic project name (e.g.
		// "devcontainer") that guess can match a stale image left by another
		// fixture and poison the metadata (e.g. a wrong remoteUser).
		inspectImage := featureImageName
		if inspectImage == "" {
			if composeConfig, cfgErr := composeClient.Config(ctx, composeFiles, ""); cfgErr == nil {
				inspectImage = composeServiceImage(composeConfig, cfg.Service)
			}
			if inspectImage == "" {
				inspectImage = fmt.Sprintf("%s-%s", projectName, cfg.Service)
			}
		}
		var metaEntries []imagemeta.Entry
		if imageInspect, inspErr := engine.InspectImage(ctx, inspectImage); inspErr == nil && imageInspect.Config != nil {
			metaEntries = imagemeta.ReadMetadataFromLabels(imageInspect.Config.Labels, logger)
		}
		metaEntries = append(metaEntries, configToMetadataEntry(cfg, opts.omitConfigRemoteEnvFromMeta))

		// Feature runtime settings — only when features were installed (unchanged).
		if withFeatures {
			merged := imagemeta.MergeConfiguration(metaEntries)

			if merged.Privileged != nil && *merged.Privileged {
				svc.SetPrivileged(true)
			}
			svc.CapAdd = merged.CapAdd
			svc.SecurityOpt = merged.SecurityOpt

			// Entrypoint — chain feature entrypoints via BuildEntrypointScriptCompose
			script := docker.BuildEntrypointScriptCompose(merged.Entrypoints)
			svc.Entrypoint = []string{"/bin/sh", "-c", script, "-"}
			svc.Command = []string{}

			if merged.Init != nil && *merged.Init {
				svc.SetInit(true)
			}
			if merged.ContainerUser != "" {
				svc.User = merged.ContainerUser
			}
			for k, v := range merged.ContainerEnv {
				svc.AddEnv(k, v) // escaping handled by ComposeOverride
			}

			// Volumes with ${devcontainerId} substitution
			if len(merged.Mounts) > 0 {
				idLabelMap := map[string]string{}
				for _, l := range idLabels {
					if i := strings.IndexByte(l, '='); i >= 0 {
						idLabelMap[l[:i]] = l[i+1:]
					}
				}
				devcontainerID := config.ComputeDevContainerID(idLabelMap)

				volumeSpecs, namedVolumes, err := composeVolumeSpecsFromMetadata(merged.Mounts, devcontainerID)
				if err != nil {
					return "", fmt.Errorf("resolve compose feature mounts: %w", err)
				}
				for _, spec := range volumeSpecs {
					svc.AddVolume(spec)
				}
				for _, volumeName := range namedVolumes {
					override.AddVolume(volumeName)
				}
			}
		}

		// Labels (always): id-labels (local_folder, config_file, --id-label) plus
		// the merged devcontainer.metadata label.
		for _, l := range idLabels {
			svc.AddLabel(l)
		}
		if metaLabel := imagemeta.GenerateMetadataLabel(metaEntries); metaLabel != "" {
			svc.AddLabel(imagemeta.MetadataLabel + "=" + metaLabel)
		}

		// Build override (persisted for container reuse) — only when there's a feature image
		if featureImageName != "" {
			buildOverride := docker.NewComposeOverride()
			buildSvc := buildOverride.Service(cfg.Service)
			buildSvc.Image = featureImageName
			buildOverridePath := filepath.Join(overrideDir, fmt.Sprintf("docker-compose.devcontainer.build-%s.json", projectName))
			if wErr := buildOverride.WriteFile(buildOverridePath); wErr != nil {
				return "", fmt.Errorf("write compose feature build override: %w", wErr)
			}
			composeFiles = append(composeFiles, buildOverridePath)
		}

		featOverridePath := filepath.Join(overrideDir, fmt.Sprintf("docker-compose.devcontainer.containerFeatures-%s.json", projectName))
		if wErr := override.WriteFile(featOverridePath); wErr != nil {
			return "", fmt.Errorf("write compose features override: %w", wErr)
		}
		composeFiles = append(composeFiles, featOverridePath)
	}

	// Build (skip if features already built the base)
	if featureImageName == "" && !noComposeBuild {
		if opts.buildNoCache {
			logger.Write("Building with --no-cache...", log.LevelInfo)
		}
		buildErr := composeClient.Build(ctx, composeFiles, "", nil, []string{cfg.Service}, opts.buildNoCache)
		if buildErr != nil {
			return "", fmt.Errorf("compose build: %w", buildErr)
		}
	}

	// Up. Match the TS CLI: when runServices is empty, pass NO service filter so
	// `compose up` brings up the whole project (all services and their
	// dependencies, e.g. a db that the app shares a network namespace with via
	// `network_mode: service:db`). Only when runServices is set do we limit to
	// those services (plus the main service). Filtering to just the app service
	// breaks reuse of a stopped project: `up --no-recreate app` does not reliably
	// restart the stopped db, so the app fails to join its network namespace.
	var services []string
	if len(cfg.RunServices) > 0 {
		services = append(services, cfg.RunServices...)
		hasService := false
		for _, s := range services {
			if s == cfg.Service {
				hasService = true
				break
			}
		}
		if !hasService {
			services = append(services, cfg.Service)
		}
	}

	noRecreate := len(existingComposeIDs) > 0 || opts.expectExistingContainer
	upErr := composeClient.Up(ctx, composeFiles, "", nil, projectName, services, noRecreate)
	if upErr != nil {
		return "", fmt.Errorf("compose up: %w", upErr)
	}

	// Find the container for the target service (all=true to include just-started containers)
	containerIDs, err := engine.ListContainers(ctx, true, []string{
		fmt.Sprintf("com.docker.compose.project=%s", projectName),
		fmt.Sprintf("com.docker.compose.service=%s", cfg.Service),
	})
	if err != nil || len(containerIDs) == 0 {
		return "", fmt.Errorf("could not find container for service %q in project %q", cfg.Service, projectName)
	}

	containerID := containerIDs[0]
	logger.Write(fmt.Sprintf("Compose container: %s", containerID[:12]), log.LevelInfo)

	// Store for JSON result
	opts.composeProjectName = projectName

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
	if probeStrategy != lifecycle.ProbeNone {
		probeServer, probeErr := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, logger)
		if probeErr == nil {
			probedEnv, _ := lifecycle.ProbeRemoteEnv(logger, probeServer, probeStrategy, remoteUser, opts.containerSessionDataFolder)
			probeServer.Close()
			for k, v := range probedEnv {
				mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, v))
			}
		}
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

	shellServer, err := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, logger, mergedRemoteEnv...)
	if err != nil {
		logger.Write(fmt.Sprintf("Could not start shell server for lifecycle hooks: %v", err), log.LevelWarning)
		return nil
	}
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

	// Marker files: make hooks idempotent across repeated `up`s and
	// editor reconnections. onCreate/updateContent/postCreate run only when their
	// marker does not already record this container's createdAt; postStart runs
	// only when the marker does not record its startedAt. postAttach always runs.
	// Matches the TS runPostCreateCommand/runPostStartCommand + updateMarkerFile.
	if markerFolder := resolveContainerUserDataFolder(shellServer); markerFolder != "" {
		createdAt := inspectResp.Created
		startedAt := ""
		if inspectResp.State != nil {
			startedAt = inspectResp.State.StartedAt
		}
		if !markerShouldRun(shellServer, markerFolder, "onCreateCommand", createdAt) {
			merged.OnCreateCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "updateContentCommand", createdAt) {
			merged.UpdateContentCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "postCreateCommand", createdAt) {
			merged.PostCreateCommands = nil
		}
		if !markerShouldRun(shellServer, markerFolder, "postStartCommand", startedAt) {
			merged.PostStartCommands = nil
		}
	}

	hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
		SkipNonBlocking: opts.skipNonBlocking,
		SkipPostAttach:  opts.skipPostAttach,
		Prebuild:        opts.prebuild,
		// Dotfiles install after postCreateCommand (before postStart), as remoteUser
		// (executor runs docker exec -u) and with secrets in the env, matching TS.
		AfterPostCreate: func() {
			if opts.dotfilesRepo != "" {
				if dfErr := lifecycle.InstallDotfiles(logger, executor, lifecycle.DotfilesConfig{
					Repository:     opts.dotfilesRepo,
					InstallCommand: opts.dotfilesCommand,
					TargetPath:     opts.dotfilesTarget,
				}); dfErr != nil {
					logger.Write(fmt.Sprintf("Dotfiles installation failed: %v", dfErr), log.LevelWarning)
				}
			}
		},
	})
	if hookErr != nil {
		phase := "postCreateCommand"
		var he *lifecycle.HookError
		if errors.As(hookErr, &he) {
			phase = string(he.Phase)
		}
		return &coreerrors.ContainerError{
			Description: fmt.Sprintf("%s from devcontainer.json failed.", phase),
		}
	}
	return nil
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
