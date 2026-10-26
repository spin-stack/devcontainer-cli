package cli

import (
	"cmp"
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
	"github.com/devcontainers/cli/internal/stringify"
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
	terminalColumns           givenInt
	terminalRows              givenInt
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
	f.Var(&opts.terminalColumns, "terminal-columns", "Terminal columns.")
	f.Var(&opts.terminalRows, "terminal-rows", "Terminal rows.")
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
	if workspaceFolder != "" || configPath != "" || overridePath != "" {
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
	containerCfg, inspectErr := inspectContainerConfig(ctx, engine, containerID)
	if inspectErr != nil {
		return readConfigurationFailure(lgr, inspectErr)
	}
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
			return writeErrorResult(out, fmt.Sprintf("compute cache key: %v", keyErr))
		}
		output["cacheKey"] = key
	}

	if ws, ok := workspaceOutput(result, workspaceFolder, opts.containerID); ok {
		output["workspace"] = orderedObject{ws, workspaceKeys}
	}

	// Include features configuration if requested (or needed for merged config without container)
	var featResult *fetchFeatureResult
	if featuresWanted(opts, containerID) {
		// TS generateFeaturesConfig (containerFeaturesConfiguration.ts) traces
		// the folder it resolves the Features from, the command's cwd.
		lgr.Write("workspace root: "+cmp.Or(workspaceFolder, cwd), log.LevelTrace)
		if result == nil || len(result.Config.Features) == 0 {
			lgr.Write("No user features to update", log.LevelTrace)
		}
	}
	if needsFeaturesConfiguration(opts, containerID, result) {
		lgr := log.New(log.Options{Level: log.ParseLevel(opts.logLevel), Format: opts.logFormat, Writer: logDst})
		var featErr error
		featResult, featErr = fetchFeatureSets(ctx, lgr, result.Config.Features, localFeatureRoots{filepath.Dir(result.Config.ConfigFilePath), workspaceFolder}, opts.skipFeatureAutoMapping)
		if featErr != nil {
			// TS readConfiguration writes the error to the log and exits 1 with no
			// result; a Feature that cannot be resolved is not an empty one.
			return readConfigurationFailure(lgr, featErr)
		}
		if featResult != nil {
			defer os.RemoveAll(featResult.TmpDir)
			output["featuresConfiguration"] = orderedObject{map[string]any{
				"featureSets": featResult.FeatureSets,
				"dstFolder":   featResult.TmpDir,
			}, featuresConfigurationKeys}
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
				return readConfigurationFailure(lgr, biErr)
			}
			base = append(base, buildInfo.Metadata...)
		}
		var featureEntries []imagemeta.Entry
		if featResult != nil {
			for _, fs := range featResult.FeatureSets {
				featureEntries = append(featureEntries, featureMetadataEntry(fs, false))
			}
		}
		entries, entriesErr := readConfigurationMetadata(append(base, featureEntries...), featureEntries, containerCfg, result, opts.idLabels, workspaceFolder, lgr)
		if entriesErr != nil {
			return readConfigurationFailure(lgr, entriesErr)
		}
		merged, mergeErr := mergedConfigurationOutput(entries, containerCfg, cfgMap)
		if mergeErr != nil {
			return mergeErr
		}
		output["mergedConfiguration"] = merged
	}

	// OCI auth diagnostics for this invocation (TS readConfiguration emits it
	// unconditionally, even when no registry was contacted).
	output["ociAuthDiagnostics"] = ociAuthDiagnostics(ctx)
	orderConfigurations(output, result)

	data, err := stringify.JSON(orderedObject{output, readConfigurationKeys}, "")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	fmt.Fprintln(out.Stdout(), string(data))
	return nil
}

// readConfigurationFailure is how read-configuration fails, as TS
// readConfiguration does: the error is logged and the command exits 1 with no
// result.
func readConfigurationFailure(lgr log.Logger, err error) error {
	lgr.Write(err.Error(), log.LevelError)
	return &coreerrors.ExitCodeError{Code: 1, Err: err}
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
	if readConfigurationHasWorkspace(opts) && workspaceFolder == "" {
		workspaceFolder = cwd
	}
	return abs(workspaceFolder), abs(opts.configPath), abs(opts.overrideConfig)
}

// readConfigurationHasWorkspace reports whether read-configuration has a
// workspace folder: --workspace-folder, else cwd unless --container-id or
// --id-label is given (TS readConfigurationOptions' check).
func readConfigurationHasWorkspace(opts *readConfigOpts) bool {
	return opts.workspaceFolder != "" || opts.containerID == "" && len(opts.idLabels) == 0
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
		// TS read-configuration exits 1 the same way on a config that is not a
		// JSON object.
		_, notFound := errors.AsType[*config.NotFoundError](err)
		_, notObject := errors.AsType[*config.NotObjectError](err)
		if notFound || notObject {
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
// VS Code URI, with the file scheme when the config was named with --config.
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
		cfgMap[k] = v
	}
	// TS makes a file URI of --config, and without a workspace of
	// --override-config, which is then the configuration (configPath =
	// overrideConfigFile); a configuration found in the workspace, or its
	// default path when only --override-config is given, has the
	// vscode-fileHost scheme, with or without --container-id
	// (devContainersSpecCLI.ts readConfiguration).
	scheme := "vscode-fileHost"
	if opts.configPath != "" || opts.overrideConfig != "" && !readConfigurationHasWorkspace(opts) {
		scheme = "file"
	}
	// TS findContainerAndIdLabels reads the URI's fsPath (devContainersSpecCLI.ts
	// readConfiguration), so read-configuration always writes it.
	cfgMap["configFilePath"] = configFileURI(result.Config.ConfigFilePath, scheme, true)
	return cfgMap
}

// configFileURI is how JSON.stringify writes the vscode-uri URI TS keeps as a
// configuration's configFilePath: its toJSON writes fsPath only once
// something has read that getter on the instance, so withFSPath says whether
// the command's TS code path did.
func configFileURI(path, scheme string, withFSPath bool) map[string]any {
	uri := map[string]any{
		"$mid":   1,
		"path":   path,
		"scheme": scheme,
	}
	if withFSPath {
		uri["fsPath"] = path
	}
	return uri
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
// container to inspect. A container that cannot be inspected is an error, as
// TS findContainerAndIdLabels' inspectContainer throws.
func inspectContainerConfig(ctx context.Context, engine *docker.EngineClient, cid string) (*container.Config, error) {
	if cid == "" {
		return nil, nil
	}
	if engine == nil {
		return nil, fmt.Errorf("inspect container %s: no Docker engine", cid)
	}
	inspect, err := engine.InspectContainer(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", cid, err)
	}
	return inspect.Config, nil
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
	// A workspaceMount the configuration sets is written as it is set, even
	// empty or null (TS readDevContainerConfigFile's `'workspaceMount' in
	// config`); otherwise the computed one, if any.
	if v, set := result.Raw["workspaceMount"]; set {
		ws["workspaceMount"] = v
	} else if result.WorkspaceConfig.WorkspaceMount != "" {
		ws["workspaceMount"] = result.WorkspaceConfig.WorkspaceMount
	}
	// The loader's additional mounts are at most the Git worktree common dir's,
	// TS's additionalMountString (getWorkspaceConfiguration).
	if mounts := result.WorkspaceConfig.AdditionalMounts; len(mounts) > 0 {
		ws["additionalMountString"] = mounts[0]
	}
	return ws, true
}

// needsFeaturesConfiguration reports whether read-configuration resolves the
// config's Features: for featuresConfiguration, or for a merged configuration
// without a container, which takes the Features' metadata (TS readConfiguration).
func needsFeaturesConfiguration(opts *readConfigOpts, containerID string, result *config.LoadResult) bool {
	return featuresWanted(opts, containerID) && result != nil && len(result.Config.Features) > 0
}

// featuresWanted reports whether TS readConfiguration reads the Features
// configuration, which it does even for a configuration with no Features.
func featuresWanted(opts *readConfigOpts, containerID string) bool {
	return opts.includeFeaturesCfg || (opts.includeMergedCfg && containerID == "")
}

// readConfigurationMetadata is the metadata read-configuration merges. With a
// container it is TS getImageMetadataFromContainer: the container's
// devcontainer.metadata entries, substituted, then the configuration's
// updateable properties for a container with every id label, else the
// Features' entries and the configuration's entry. Without one it is base (the
// image's and the Features' metadata) and the configuration's entry.
func readConfigurationMetadata(base, featureEntries []imagemeta.Entry, containerCfg *container.Config, result *config.LoadResult, providedIDLabels []string, workspaceFolder string, lgr log.Logger) ([]imagemeta.Entry, error) {
	var cfg *config.DevContainer
	hostSub, configFile := config.EnvHostSubContext(), ""
	if result != nil {
		cfg, hostSub, configFile = result.Config, result.HostSub, result.Config.ConfigFilePath
	}
	if containerCfg == nil {
		if cfg == nil {
			return base, nil
		}
		return append(base, configToMetadataEntry(cfg)), nil
	}
	idLabels := containerIDLabels(providedIDLabels, containerCfg.Labels, workspaceFolder, configFile)
	return imageMetadataFromContainer(containerCfg.Labels, cfg, featureEntries, idLabels, hostMetadataSubstitution(hostSub, idLabels), lgr)
}

// mergedConfigurationOutput is read-configuration's "mergedConfiguration": the
// merge of entries (readConfigurationMetadata). With a container,
// ${containerEnv:...} is resolved with its environment. TS builds
// mergedConfiguration by spreading the config, so the properties
// mergeConfiguration does not own (image, features, build, configFilePath, ...)
// come from cfgMap.
func mergedConfigurationOutput(entries []imagemeta.Entry, containerCfg *container.Config, cfgMap map[string]any) (map[string]any, error) {
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
