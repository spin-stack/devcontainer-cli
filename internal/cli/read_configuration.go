package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

// mergedOwnKeys are the properties TS mergeConfiguration sets itself, from the
// metadata, or removes from the config (replaceProperties) - in imageMetadata.ts.
// Every other property of the config passes into mergedConfiguration as it is
// ({...config}); one of these never does, even when the merge leaves it unset.
var mergedOwnKeys = map[string]bool{
	"customizations": true, "entrypoint": true, "onCreateCommand": true, "updateContentCommand": true,
	"postCreateCommand": true, "postStartCommand": true, "postAttachCommand": true, "shutdownAction": true,
	"init": true, "privileged": true, "capAdd": true, "securityOpt": true, "entrypoints": true, "mounts": true,
	"onCreateCommands": true, "updateContentCommands": true, "postCreateCommands": true,
	"postStartCommands": true, "postAttachCommands": true, "waitFor": true, "remoteUser": true,
	"containerUser": true, "userEnvProbe": true, "remoteEnv": true, "containerEnv": true,
	"overrideCommand": true, "portsAttributes": true, "otherPortsAttributes": true, "forwardPorts": true,
	"updateRemoteUserUID": true, "hostRequirements": true,
}

type readConfigOpts struct {
	workspaceFolder           string
	configPath                string
	overrideConfig            string
	mountWorkspaceGitRoot     bool
	mountGitWorktreeCommonDir bool
	includeFeaturesCfg        bool
	includeMergedCfg          bool
	logLevel                  string
	logFormat                 string
	logFile                   string
	terminalLogFile           string
	containerID               string
	idLabels                  []string
	dockerPath                string
	additionalFeatures        string
	skipFeatureAutoMapping    bool
	terminalColumns           int
	terminalRows              int
	cacheKey                  bool
}

func newReadConfigurationCmd() *cobra.Command {
	var opts readConfigOpts

	cmd := &cobra.Command{
		Use:   "read-configuration",
		Short: "Read configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReadConfiguration(cmd.Context(), outputFor(cmd), &opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.overrideConfig, "override-config", "", "devcontainer.json path to override.")
	f.BoolVar(&opts.mountWorkspaceGitRoot, "mount-workspace-git-root", true, "Mount the workspace using its Git root.")
	f.BoolVar(&opts.mountGitWorktreeCommonDir, "mount-git-worktree-common-dir", false, "Mount the Git worktree common dir for Git operations to work in the container.")
	f.BoolVar(&opts.includeFeaturesCfg, "include-features-configuration", false, "Include features configuration.")
	f.BoolVar(&opts.includeMergedCfg, "include-merged-configuration", false, "Include merged configuration.")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")

	// Container-related flags for containerEnv substitution
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	f.String("docker-compose-path", "", "Docker Compose CLI path.")
	f.String("user-data-folder", "", "Host path to persisted data.")
	f.StringVar(&opts.containerID, "container-id", "", "Container ID.")
	f.StringArrayVar(&opts.idLabels, "id-label", nil, "Id label(s).")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "Terminal columns.")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "Terminal rows.")
	f.StringVar(&opts.additionalFeatures, "additional-features", "", "Additional features JSON.")
	f.BoolVar(&opts.skipFeatureAutoMapping, "skip-feature-auto-mapping", false, "")
	cmd.Flags().MarkHidden("skip-feature-auto-mapping")
	// Go-only: emit a deterministic content-addressed cache key alongside the
	// configuration (for prebuild reuse by external orchestrators). Additive — the
	// default output is unchanged from TS.
	f.BoolVar(&opts.cacheKey, "cache-key", false, "Also emit a deterministic content-addressed cache key for the configuration.")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

func runReadConfiguration(ctx context.Context, out Output, opts *readConfigOpts) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}

	if err := validateEnums(
		enumFlag{"log-level", opts.logLevel, logLevels},
		enumFlag{"log-format", opts.logFormat, logFormats},
	); err != nil {
		return err
	}
	if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
		return err
	}

	logDst, closeLog, logErr := logWriter(opts.logFile, opts.terminalLogFile)
	if logErr != nil {
		return fmt.Errorf("open log file: %w", logErr)
	}
	defer closeLog()

	workspaceFolder := opts.workspaceFolder
	// 0.88: default --workspace-folder to cwd when no --container-id/--id-label/--workspace-folder.
	if workspaceFolder == "" && opts.containerID == "" && len(opts.idLabels) == 0 {
		workspaceFolder, _ = os.Getwd()
	}

	// Resolve to absolute path
	if workspaceFolder != "" && !filepath.IsAbs(workspaceFolder) {
		cwd, _ := os.Getwd()
		workspaceFolder = filepath.Join(cwd, workspaceFolder)
	}

	configPath := opts.configPath
	if configPath != "" && !filepath.IsAbs(configPath) {
		cwd, _ := os.Getwd()
		configPath = filepath.Join(cwd, configPath)
	}

	overridePath := opts.overrideConfig
	if overridePath != "" && !filepath.IsAbs(overridePath) {
		cwd, _ := os.Getwd()
		overridePath = filepath.Join(cwd, overridePath)
	}

	var result *config.LoadResult
	if workspaceFolder != "" || configPath != "" {
		var loadErr error
		result, loadErr = config.LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overridePath, config.MountOptions{
			MountWorkspaceGitRoot:     opts.mountWorkspaceGitRoot,
			MountGitWorktreeCommonDir: opts.mountGitWorktreeCommonDir,
		})
		if loadErr != nil {
			// 0.88: read-configuration exits 1 silently (banner only) when no
			// config is found — it does not print an error envelope. Parse/other
			// errors still surface normally.
			if _, ok := errors.AsType[*config.NotFoundError](loadErr); ok {
				return &coreerrors.ExitCodeError{Code: 1}
			}
			return loadErr
		}

		if _, err := mergeAdditionalFeatures(result.Config, opts.additionalFeatures); err != nil {
			return fmt.Errorf("additional-features: %w", err)
		}

		result.Config.Extensions = nil
		result.Config.Settings = nil
		result.Config.DevPort = nil
	}

	// Build config map for output
	var cfgMap map[string]any
	if result != nil {
		cfgJSON, _ := json.Marshal(result.Config)
		json.Unmarshal(cfgJSON, &cfgMap)
		for k, v := range cfgMap {
			if v == nil {
				delete(cfgMap, k)
			}
		}
		// Preserve unknown/vendor top-level properties that the typed struct drops:
		// the TS config output keeps them. Legacy fields migrated
		// into customizations (settings/extensions/devPort) stay removed.
		for k, v := range result.Raw {
			if _, known := cfgMap[k]; known {
				continue
			}
			if k == "settings" || k == "extensions" || k == "devPort" {
				continue
			}
			cfgMap[k] = v
		}
		// Emit configFilePath as VS Code URI object (matches TS output)
		if cfp, ok := cfgMap["configFilePath"].(string); ok && cfp != "" {
			scheme := "vscode-fileHost"
			if opts.containerID != "" || opts.configPath != "" || opts.overrideConfig != "" {
				scheme = "file"
			}
			cfgMap["configFilePath"] = map[string]any{
				"$mid":   1,
				"fsPath": cfp,
				"path":   cfp,
				"scheme": scheme,
			}
		} else {
			delete(cfgMap, "configFilePath")
		}
	} else {
		cfgMap = map[string]any{} // No config → empty object
	}

	// Find container for containerEnv substitution
	lgr := log.New(log.Options{Version: cliVersion(), Level: log.ParseLevel(opts.logLevel), Format: opts.logFormat, Writer: logDst})
	engine, engineErr := docker.NewEngineClient(lgr)
	if engineErr != nil {
		// Non-fatal: container lookup features won't work but config can still be read.
		lgr.Write(fmt.Sprintf("Warning: Docker engine: %v", engineErr), log.LevelWarning)
	}
	if engine != nil {
		defer engine.Close()
	}

	containerID := opts.containerID
	if containerID == "" && engine != nil {
		configFile := ""
		if result != nil {
			configFile = result.Config.ConfigFilePath
		}
		// [local_folder, config_file] so multiple configs in one project resolve
		// to the right container (fallback to local_folder for legacy ones).
		containerID = resolveContainerID(ctx, engine, workspaceFolder, configFile, opts.idLabels)
	}

	if containerID != "" && engine != nil {
		inspect, inspectErr := engine.InspectContainer(ctx, containerID)
		if inspectErr == nil && inspect.Config != nil {
			cEnv := config.EnvMap(inspect.Config.Env)
			substituted, subErr := resolveContainerVariables(cEnv, cfgMap)
			if subErr != nil {
				return writeErrorResult(out, fmt.Sprintf("resolve configuration container variables: %v", subErr))
			}
			if subMap, ok := substituted.(map[string]any); ok {
				cfgMap = subMap
			}
		}
	}

	output := map[string]any{
		"configuration": cfgMap,
	}

	// Go-only: deterministic content-addressed cache key (additive; off by default).
	if opts.cacheKey && result != nil {
		key, keyErr := computeCacheKey(result, proxyEnvFromEnviron())
		if keyErr != nil {
			return fmt.Errorf("compute cache key: %w", keyErr)
		}
		output["cacheKey"] = key
	}

	if result != nil && result.WorkspaceConfig != nil {
		if workspaceFolder == "" && opts.containerID != "" {
			// container-id without workspace-folder: emit empty workspace (matches TS)
			output["workspace"] = map[string]any{}
		} else {
			ws := map[string]any{
				"workspaceFolder": result.WorkspaceConfig.WorkspaceFolder,
			}
			if result.WorkspaceConfig.WorkspaceMount != "" {
				ws["workspaceMount"] = result.WorkspaceConfig.WorkspaceMount
			}
			output["workspace"] = ws
		}
	}

	// Include features configuration if requested (or needed for merged config without container)
	needsFeaturesConfig := opts.includeFeaturesCfg || (opts.includeMergedCfg && containerID == "")
	if needsFeaturesConfig && result != nil && len(result.Config.Features) > 0 {
		lgr := log.New(log.Options{Level: log.ParseLevel(opts.logLevel), Format: opts.logFormat, Writer: logDst})
		featResult, featErr := fetchFeatureSets(ctx, lgr, result.Config.Features, filepath.Dir(result.Config.ConfigFilePath), opts.skipFeatureAutoMapping)
		if featErr != nil {
			// TS readConfiguration writes the error to the log and exits 1 with no
			// result; a Feature that cannot be resolved is not an empty one.
			lgr.Write(featErr.Error(), log.LevelError)
			return &coreerrors.ExitCodeError{Code: 1, Err: featErr}
		}
		if featResult != nil {
			defer os.RemoveAll(featResult.TmpDir)
			output["featuresConfiguration"] = map[string]any{
				"featureSets": featResult.FeatureSets,
				"dstFolder":   featResult.TmpDir,
			}
		}
	}

	// Include merged configuration if requested
	if opts.includeMergedCfg {
		// Collect metadata entries: image labels + config as last entry
		var entries []imagemeta.Entry

		// If we have a container, read image metadata labels
		cid := containerID
		if cid == "" && opts.containerID != "" {
			cid = opts.containerID
		}
		if cid != "" && engine != nil {
			inspect, insErr := engine.InspectContainer(ctx, cid)
			if insErr == nil && inspect.Config != nil {
				entries = imagemeta.ReadMetadataFromLabels(inspect.Config.Labels, lgr)
			}
		} else if cid == "" {
			// No container yet: derive the base metadata from the image (the base
			// image of a Dockerfile, the compose service's image or build) and the
			// features, like TS readConfiguration does with getImageBuildInfo and
			// getDevcontainerMetadata. VS Code calls this before creating the
			// container. The image is inspected locally, else in its registry, else
			// pulled; a failure is an error, as in TS.
			buildInfo, biErr := readConfigurationImageBuildInfo(ctx, lgr, engine, opts.dockerPath, result)
			if biErr != nil {
				lgr.Write(biErr.Error(), log.LevelError)
				return &coreerrors.ExitCodeError{Code: 1, Err: biErr}
			}
			entries = append(entries, buildInfo.Metadata...)
			if result != nil && len(result.Config.Features) > 0 {
				if fr, ferr := fetchFeatureSets(ctx, lgr, result.Config.Features, filepath.Dir(result.Config.ConfigFilePath), opts.skipFeatureAutoMapping); ferr == nil && fr != nil {
					defer os.RemoveAll(fr.TmpDir)
					for _, fs := range fr.FeatureSets {
						entries = append(entries, featureMetadataEntry(fs, false))
					}
				}
			}
		}

		// Add the config itself as the last entry (highest priority)
		if result != nil {
			configEntry := configToMetadataEntry(result.Config)
			entries = append(entries, configEntry)
		}

		merged := imagemeta.MergeConfiguration(entries)

		// mergedConfiguration spreads the config (TS), so image comes from the
		// config, not from the metadata entries (which no longer carry it).
		if result != nil {
			merged.Image = result.Config.Image
		}

		// Copy configFilePath URI to mergedConfiguration (matches TS behavior)
		if cfpURI, ok := cfgMap["configFilePath"]; ok {
			merged.ConfigFilePath = cfpURI
		}

		if cid != "" && engine != nil {
			inspect, insErr := engine.InspectContainer(ctx, cid)
			if insErr == nil && inspect.Config != nil {
				cEnv := config.EnvMap(inspect.Config.Env)
				mergedJSON, _ := json.Marshal(merged)
				var mergedGeneric any
				json.Unmarshal(mergedJSON, &mergedGeneric)
				substituted, subErr := resolveContainerVariables(cEnv, mergedGeneric)
				if subErr != nil {
					return subErr
				}
				subJSON, _ := json.Marshal(substituted)
				json.Unmarshal(subJSON, merged)
			}
		}

		// TS builds mergedConfiguration by spreading the config, so config-only
		// properties that the typed MergedConfig does not model (features, build,
		// service, workspaceFolder, …) must be carried over.
		mergedJSON, _ := json.Marshal(merged)
		var mergedMap map[string]any
		if json.Unmarshal(mergedJSON, &mergedMap) == nil {
			for k, v := range cfgMap {
				if !mergedOwnKeys[k] {
					mergedMap[k] = v
				}
			}
			output["mergedConfiguration"] = mergedMap
		} else {
			output["mergedConfiguration"] = merged
		}
	}

	// OCI auth diagnostics for this invocation (TS readConfiguration emits it
	// unconditionally, even when no registry was contacted).
	output["ociAuthDiagnostics"] = ociAuthDiagnostics(ctx)

	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	fmt.Fprintln(out.Stdout(), string(data))
	return nil
}

// readConfigurationImageBuildInfo runs getImageBuildInfo for read-configuration's
// merged configuration without a container. Without a configuration TS reads an
// empty one, which names no image.
func readConfigurationImageBuildInfo(ctx context.Context, lgr log.Logger, engine *docker.EngineClient, dockerPath string, result *config.LoadResult) (*imageBuildInfo, error) {
	cfg := &config.DevContainer{}
	if result != nil {
		cfg = result.Config
	}
	composeConfig := func(ctx context.Context, files []string, envFile string) (map[string]any, error) {
		client, err := docker.NewComposeClient(dockerPath, "", nil, lgr)
		if err != nil {
			return nil, err
		}
		return client.Config(ctx, files, envFile)
	}
	return getImageBuildInfo(ctx, cfg, newImageInspector(ctx, lgr, engine), composeConfig, lgr)
}

// configToMetadataEntry converts a DevContainer to a metadata Entry
// for merging. When omitRemoteEnv is true, remoteEnv is excluded from the
// entry (used for image metadata labels with --omit-config-remote-env-from-metadata).
// configToMetadataEntry builds the devcontainer.metadata entry for cfg,
// including its remoteEnv. Use configMetadataEntry when remoteEnv must be
// omitted (a runtime decision on some up/build paths).
func configToMetadataEntry(cfg *config.DevContainer) imagemeta.Entry {
	return configMetadataEntry(cfg, false)
}

func configMetadataEntry(cfg *config.DevContainer, omitRemoteEnv bool) imagemeta.Entry {
	entry := imagemeta.Entry{
		RemoteUser:          cfg.RemoteUser,
		ContainerUser:       cfg.ContainerUser,
		UserEnvProbe:        cfg.UserEnvProbe,
		OverrideCommand:     cfg.OverrideCommand,
		Init:                cfg.Init,
		Privileged:          cfg.Privileged,
		WaitFor:             cfg.WaitFor,
		ShutdownAction:      cfg.ShutdownAction,
		UpdateRemoteUserUID: cfg.UpdateRemoteUserUID,
		RunArgs:             cfg.RunArgs,
		ContainerEnv:        cfg.ContainerEnv,
		CapAdd:              cfg.CapAdd,
		SecurityOpt:         cfg.SecurityOpt,
		Mounts:              metadataMounts(cfg),
		Customizations:      cfg.Customizations,
	}
	// Port and host-requirement properties are part of the config metadata entry
	// (pickConfigProperties in TS). Omitting them made the devcontainer.metadata
	// label lossy vs the TS CLI.
	if len(cfg.ForwardPorts) > 0 {
		fp := make([]any, len(cfg.ForwardPorts))
		for i, p := range cfg.ForwardPorts {
			fp[i] = p.Value()
		}
		entry.ForwardPorts = fp
	}
	if len(cfg.PortsAttributes) > 0 {
		pa := make(map[string]any, len(cfg.PortsAttributes))
		for k, v := range cfg.PortsAttributes {
			pa[k] = v
		}
		entry.PortsAttributes = pa
	}
	if cfg.OtherPortsAttributes != nil {
		entry.OtherPortsAttributes = cfg.OtherPortsAttributes
	}
	if cfg.HostRequirements != nil {
		entry.HostRequirements = cfg.HostRequirements
	}
	if cfg.RemoteEnv != nil && !omitRemoteEnv {
		entry.RemoteEnv = cfg.RemoteEnv
	}
	// Lifecycle commands
	if !cfg.OnCreateCommand.IsEmpty() {
		entry.OnCreateCommand = cfg.OnCreateCommand.Raw()
	}
	if !cfg.UpdateContentCommand.IsEmpty() {
		entry.UpdateContentCommand = cfg.UpdateContentCommand.Raw()
	}
	if !cfg.PostCreateCommand.IsEmpty() {
		entry.PostCreateCommand = cfg.PostCreateCommand.Raw()
	}
	if !cfg.PostStartCommand.IsEmpty() {
		entry.PostStartCommand = cfg.PostStartCommand.Raw()
	}
	if !cfg.PostAttachCommand.IsEmpty() {
		entry.PostAttachCommand = cfg.PostAttachCommand.Raw()
	}
	return entry
}
