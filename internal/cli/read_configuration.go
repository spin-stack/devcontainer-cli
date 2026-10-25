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
	"github.com/moby/moby/api/types/container"
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

	cwd, _ := os.Getwd()
	workspaceFolder, configPath, overridePath := readConfigurationPaths(opts, cwd)

	var result *config.LoadResult
	if workspaceFolder != "" || configPath != "" {
		var loadErr error
		if result, loadErr = loadReadConfiguration(workspaceFolder, configPath, overridePath, opts); loadErr != nil {
			return loadErr
		}
	}

	cfgMap := configurationOutput(result, opts)

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

	containerID := readConfigurationContainerID(ctx, engine, opts, workspaceFolder, result)
	containerCfg := inspectContainerConfig(ctx, engine, containerID)
	cfgMap, subErr := substituteContainerEnv(containerCfg, cfgMap)
	if subErr != nil {
		return writeErrorResult(out, fmt.Sprintf("resolve configuration container variables: %v", subErr))
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

	if ws, ok := workspaceOutput(result, workspaceFolder, opts.containerID); ok {
		output["workspace"] = ws
	}

	// Include features configuration if requested (or needed for merged config without container)
	var featResult *fetchFeatureResult
	if needsFeaturesConfiguration(opts, containerID, result) {
		lgr := log.New(log.Options{Level: log.ParseLevel(opts.logLevel), Format: opts.logFormat, Writer: logDst})
		var featErr error
		featResult, featErr = fetchFeatureSets(ctx, lgr, result.Config.Features, filepath.Dir(result.Config.ConfigFilePath), opts.skipFeatureAutoMapping)
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
		var base []imagemeta.Entry
		if containerID == "" {
			// No container yet: derive the base metadata from the image (the base
			// image of a Dockerfile, the compose service's image or build) and the
			// features, like TS readConfiguration does with getImageBuildInfo and
			// getDevcontainerMetadata(imageBuildInfo.metadata, config,
			// featuresConfig). VS Code calls this before creating the container.
			// The image is inspected locally, else in its registry, else pulled; a
			// failure is an error, as in TS. The Features are the ones resolved for
			// featuresConfiguration above, as in TS.
			buildInfo, biErr := readConfigurationImageBuildInfo(ctx, lgr, engine, opts.dockerPath, result)
			if biErr != nil {
				lgr.Write(biErr.Error(), log.LevelError)
				return &coreerrors.ExitCodeError{Code: 1, Err: biErr}
			}
			base = append(base, buildInfo.Metadata...)
			if featResult != nil {
				for _, fs := range featResult.FeatureSets {
					base = append(base, featureMetadataEntry(fs, false))
				}
			}
		}
		merged, mergeErr := mergedConfigurationOutput(base, containerCfg, result, cfgMap, lgr)
		if mergeErr != nil {
			return mergeErr
		}
		output["mergedConfiguration"] = merged
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

// readConfigurationPaths resolves --workspace-folder, --config and
// --override-config against cwd. Without --container-id, --id-label or
// --workspace-folder, the workspace folder is cwd (0.88).
func readConfigurationPaths(opts *readConfigOpts, cwd string) (workspaceFolder, configPath, overridePath string) {
	abs := func(p string) string {
		if p != "" && !filepath.IsAbs(p) {
			return filepath.Join(cwd, p)
		}
		return p
	}
	workspaceFolder = opts.workspaceFolder
	if workspaceFolder == "" && opts.containerID == "" && len(opts.idLabels) == 0 {
		workspaceFolder = cwd
	}
	return abs(workspaceFolder), abs(opts.configPath), abs(opts.overrideConfig)
}

// loadReadConfiguration loads the configuration read-configuration prints,
// with the --additional-features merged in.
func loadReadConfiguration(workspaceFolder, configPath, overridePath string, opts *readConfigOpts) (*config.LoadResult, error) {
	result, err := config.LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overridePath, config.MountOptions{
		MountWorkspaceGitRoot:     opts.mountWorkspaceGitRoot,
		MountGitWorktreeCommonDir: opts.mountGitWorktreeCommonDir,
	})
	if err != nil {
		// 0.88: read-configuration exits 1 silently (banner only) when no
		// config is found — it does not print an error envelope. Parse/other
		// errors still surface normally.
		if _, ok := errors.AsType[*config.NotFoundError](err); ok {
			return nil, &coreerrors.ExitCodeError{Code: 1}
		}
		return nil, err
	}
	if _, err := mergeAdditionalFeatures(result.Config, opts.additionalFeatures); err != nil {
		return nil, fmt.Errorf("additional-features: %w", err)
	}
	return result, nil
}

// configurationOutput is read-configuration's "configuration": the config as
// TS prints it, an empty object without one. Unknown and vendor properties the
// typed config drops are kept. The legacy settings, extensions and devPort are
// left out only where the loader migrated them into customizations: one it
// left in place, as TS updateFromOldProperties does, is printed. configFilePath is a
// VS Code URI, with the file scheme when the config was named on the command
// line or a container was.
func configurationOutput(result *config.LoadResult, opts *readConfigOpts) map[string]any {
	cfgMap := map[string]any{}
	if result == nil {
		return cfgMap
	}
	cfgJSON, _ := json.Marshal(result.Config)
	json.Unmarshal(cfgJSON, &cfgMap)
	for k, v := range cfgMap {
		if v == nil {
			delete(cfgMap, k)
		}
	}
	for k, v := range result.Raw {
		if _, known := cfgMap[k]; known {
			continue
		}
		// TS moves a null devPort into customizations.vscode, which the loader
		// does not; it is never printed at the top level.
		if k == "devPort" && v == nil {
			continue
		}
		cfgMap[k] = v
	}
	scheme := "vscode-fileHost"
	if opts.containerID != "" || opts.configPath != "" || opts.overrideConfig != "" {
		scheme = "file"
	}
	cfp := result.Config.ConfigFilePath
	cfgMap["configFilePath"] = map[string]any{
		"$mid":   1,
		"fsPath": cfp,
		"path":   cfp,
		"scheme": scheme,
	}
	return cfgMap
}

// readConfigurationContainerID is the container read-configuration reads: the
// --container-id, else the one the id labels or the workspace folder and
// config file find. Without a Docker engine, only the --container-id.
func readConfigurationContainerID(ctx context.Context, engine *docker.EngineClient, opts *readConfigOpts, workspaceFolder string, result *config.LoadResult) string {
	if opts.containerID != "" || engine == nil {
		return opts.containerID
	}
	configFile := ""
	if result != nil {
		configFile = result.Config.ConfigFilePath
	}
	// [local_folder, config_file] so multiple configs in one project resolve
	// to the right container (fallback to local_folder for legacy ones).
	return resolveContainerID(ctx, engine, workspaceFolder, configFile, opts.idLabels)
}

// inspectContainerConfig is the config of container cid, nil when there is no
// container to inspect or it cannot be inspected.
func inspectContainerConfig(ctx context.Context, engine *docker.EngineClient, cid string) *container.Config {
	if cid == "" || engine == nil {
		return nil
	}
	inspect, err := engine.InspectContainer(ctx, cid)
	if err != nil {
		return nil
	}
	return inspect.Config
}

// substituteContainerEnv resolves ${containerEnv:...} in cfgMap with the
// container's environment; without a container cfgMap is returned as it is.
func substituteContainerEnv(containerCfg *container.Config, cfgMap map[string]any) (map[string]any, error) {
	if containerCfg == nil {
		return cfgMap, nil
	}
	substituted, err := resolveContainerVariables(config.EnvMap(containerCfg.Env), cfgMap)
	if err != nil {
		return nil, err
	}
	if subMap, ok := substituted.(map[string]any); ok {
		return subMap, nil
	}
	return cfgMap, nil
}

// workspaceOutput is read-configuration's "workspace"; ok is false when the
// configuration gives none. With --container-id and no workspace folder it is
// empty, as in TS.
func workspaceOutput(result *config.LoadResult, workspaceFolder, containerIDFlag string) (ws map[string]any, ok bool) {
	if result == nil || result.WorkspaceConfig == nil {
		return nil, false
	}
	if workspaceFolder == "" && containerIDFlag != "" {
		return map[string]any{}, true
	}
	ws = map[string]any{
		"workspaceFolder": result.WorkspaceConfig.WorkspaceFolder,
	}
	if result.WorkspaceConfig.WorkspaceMount != "" {
		ws["workspaceMount"] = result.WorkspaceConfig.WorkspaceMount
	}
	return ws, true
}

// needsFeaturesConfiguration reports whether read-configuration resolves the
// config's Features: for featuresConfiguration, or for a merged configuration
// without a container, which takes the Features' metadata (TS readConfiguration).
func needsFeaturesConfiguration(opts *readConfigOpts, containerID string, result *config.LoadResult) bool {
	wanted := opts.includeFeaturesCfg || (opts.includeMergedCfg && containerID == "")
	return wanted && result != nil && len(result.Config.Features) > 0
}

// mergedConfigurationOutput is read-configuration's "mergedConfiguration". The
// metadata entries are the container's devcontainer.metadata label when there is
// a container, else base (the image's and the Features' metadata); the config is
// the last, highest-priority entry. With a container, ${containerEnv:...} is
// resolved with its environment. TS builds mergedConfiguration by spreading the
// config, so the properties mergeConfiguration does not own (image, features,
// build, configFilePath, ...) come from cfgMap.
func mergedConfigurationOutput(base []imagemeta.Entry, containerCfg *container.Config, result *config.LoadResult, cfgMap map[string]any, lgr log.Logger) (map[string]any, error) {
	entries := base
	if containerCfg != nil {
		entries = imagemeta.ReadMetadataFromLabels(containerCfg.Labels, lgr)
	}
	if result != nil {
		entries = append(entries, configToMetadataEntry(result.Config))
	}
	merged := imagemeta.MergeConfiguration(entries)

	if containerCfg != nil {
		mergedJSON, _ := json.Marshal(merged)
		var mergedGeneric any
		json.Unmarshal(mergedJSON, &mergedGeneric)
		substituted, err := resolveContainerVariables(config.EnvMap(containerCfg.Env), mergedGeneric)
		if err != nil {
			return nil, err
		}
		subJSON, _ := json.Marshal(substituted)
		json.Unmarshal(subJSON, merged)
	}

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged configuration: %w", err)
	}
	var mergedMap map[string]any
	if err := json.Unmarshal(mergedJSON, &mergedMap); err != nil {
		return nil, fmt.Errorf("read merged configuration: %w", err)
	}
	for k, v := range cfgMap {
		if !mergedOwnKeys[k] {
			mergedMap[k] = v
		}
	}
	return mergedMap, nil
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
