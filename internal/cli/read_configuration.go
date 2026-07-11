package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/spf13/cobra"
)

// mergedPassthroughKeys are config-only properties spread into mergedConfiguration
// by the TS CLI that the typed MergedConfig does not model. They are copied from
// the raw config into the merged output (blocker B12).
var mergedPassthroughKeys = []string{
	"features", "build", "dockerComposeFile", "service", "workspaceFolder",
	"name", "overrideFeatureInstallOrder",
}

type readConfigOpts struct {
	workspaceFolder        string
	configPath             string
	overrideConfig         string
	mountWorkspaceGitRoot  bool
	includeFeaturesCfg     bool
	includeMergedCfg       bool
	logLevel               string
	logFormat              string
	containerID            string
	idLabels               []string
	dockerPath             string
	additionalFeatures     string
	skipFeatureAutoMapping bool
	terminalColumns        int
	terminalRows           int
}

func newReadConfigurationCmd() *cobra.Command {
	var opts readConfigOpts

	cmd := &cobra.Command{
		Use:   "read-configuration",
		Short: "Read configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReadConfiguration(&opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.overrideConfig, "override-config", "", "devcontainer.json path to override.")
	f.BoolVar(&opts.mountWorkspaceGitRoot, "mount-workspace-git-root", true, "Mount the workspace using its Git root.")
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

	addLogFileFlags(cmd)
	return cmd
}

func runReadConfiguration(opts *readConfigOpts) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}

	for _, v := range []struct {
		flag, val string
		choices   []string
	}{
		{"log-level", opts.logLevel, []string{"info", "debug", "trace"}},
		{"log-format", opts.logFormat, []string{"text", "json"}},
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return err
		}
	}
	if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
		return err
	}

	workspaceFolder := opts.workspaceFolder
	if workspaceFolder == "" && opts.containerID == "" && len(opts.idLabels) == 0 {
		return fmt.Errorf("Missing required argument: workspace-folder or id-label or container-id")
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
		result, loadErr = config.LoadDevContainerConfig(workspaceFolder, configPath, overridePath)
		if loadErr != nil {
			return loadErr
		}

		if err := mergeAdditionalFeatures(result.Config, opts.additionalFeatures); err != nil {
			return fmt.Errorf("additional-features: %w", err)
		}

		result.Config.Extensions = nil
		result.Config.Settings = nil
		result.Config.DevPort = nil
	}

	// Build config map for output
	var cfgMap map[string]interface{}
	if result != nil {
		cfgJSON, _ := json.Marshal(result.Config)
		json.Unmarshal(cfgJSON, &cfgMap)
		for k, v := range cfgMap {
			if v == nil {
				delete(cfgMap, k)
			}
		}
		// Preserve unknown/vendor top-level properties that the typed struct drops
		// (blocker B12): the TS config output keeps them. Legacy fields migrated
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
			cfgMap["configFilePath"] = map[string]interface{}{
				"$mid":   1,
				"fsPath": cfp,
				"path":   cfp,
				"scheme": scheme,
			}
		} else {
			delete(cfgMap, "configFilePath")
		}
	} else {
		cfgMap = map[string]interface{}{} // No config → empty object
	}

	// Find container for containerEnv substitution
	ctx := context.Background()
	lgr := log.New(log.Options{Version: cliVersion(), Level: log.MapLogLevel(opts.logLevel), Format: opts.logFormat, Writer: os.Stderr, Dimensions: logDimensions(opts.terminalColumns, opts.terminalRows)})
	engine, engineErr := docker.NewEngineClient(lgr)
	if engineErr != nil {
		// Non-fatal: container lookup features won't work but config can still be read.
		lgr.Write(fmt.Sprintf("Warning: Docker engine: %v", engineErr), log.LevelWarning)
	}
	if engine != nil {
		defer engine.Close()
	}

	containerID := opts.containerID
	if containerID == "" && engine != nil && len(opts.idLabels) > 0 {
		ids, _ := engine.ListContainers(ctx, true, opts.idLabels)
		if len(ids) > 0 {
			containerID = ids[0]
		}
	}
	if containerID == "" && engine != nil && workspaceFolder != "" {
		labels := []string{fmt.Sprintf("devcontainer.local_folder=%s", workspaceFolder)}
		ids, _ := engine.ListContainers(ctx, false, labels)
		if len(ids) > 0 {
			containerID = ids[0]
		}
	}

	if containerID != "" && engine != nil {
		inspect, inspectErr := engine.InspectContainer(ctx, containerID)
		if inspectErr == nil && inspect.Config != nil {
			cEnv := envSliceToMap(inspect.Config.Env)
			substituted := config.SubstituteContainer("linux", cEnv, cfgMap)
			if subMap, ok := substituted.(map[string]interface{}); ok {
				cfgMap = subMap
			}
		}
	}

	output := map[string]interface{}{
		"configuration": cfgMap,
	}

	if result != nil && result.WorkspaceConfig != nil {
		if workspaceFolder == "" && opts.containerID != "" {
			// container-id without workspace-folder: emit empty workspace (matches TS)
			output["workspace"] = map[string]interface{}{}
		} else {
			ws := map[string]interface{}{
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
		lgr := log.New(log.Options{Level: log.MapLogLevel(opts.logLevel), Format: opts.logFormat, Writer: os.Stderr, Dimensions: logDimensions(opts.terminalColumns, opts.terminalRows)})
		featResult, featErr := fetchFeatureSets(lgr, result.Config.Features, filepath.Dir(result.Config.ConfigFilePath), opts.skipFeatureAutoMapping, nil)
		if featErr == nil && featResult != nil {
			defer os.RemoveAll(featResult.TmpDir)
			output["featuresConfiguration"] = map[string]interface{}{
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
		} else if result != nil && engine != nil {
			// No container yet: derive the base metadata from the image and the
			// features, like the TS getImageBuildInfo. Otherwise mergedConfiguration
			// loses the image's remoteUser/lifecycle/customizations and the features'
			// metadata before the container is created (blocker B13) — VS Code calls
			// this before creating the container.
			if result.Config.Image != "" {
				if img, imgErr := engine.InspectImage(ctx, result.Config.Image); imgErr == nil && img.Config != nil {
					entries = append(entries, imagemeta.ReadMetadataFromLabels(img.Config.Labels, lgr)...)
				}
			}
			if len(result.Config.Features) > 0 {
				if fr, ferr := fetchFeatureSets(lgr, result.Config.Features, filepath.Dir(result.Config.ConfigFilePath), opts.skipFeatureAutoMapping, nil); ferr == nil && fr != nil {
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
				cEnv := envSliceToMap(inspect.Config.Env)
				mergedJSON, _ := json.Marshal(merged)
				var mergedGeneric interface{}
				json.Unmarshal(mergedJSON, &mergedGeneric)
				substituted := config.SubstituteContainer("linux", cEnv, mergedGeneric)
				subJSON, _ := json.Marshal(substituted)
				json.Unmarshal(subJSON, merged)
			}
		}

		// TS builds mergedConfiguration by spreading the config, so config-only
		// properties that the typed MergedConfig does not model (features, build,
		// service, workspaceFolder, …) must be carried over (blocker B12).
		mergedJSON, _ := json.Marshal(merged)
		var mergedMap map[string]interface{}
		if json.Unmarshal(mergedJSON, &mergedMap) == nil {
			for _, k := range mergedPassthroughKeys {
				if v, ok := cfgMap[k]; ok {
					mergedMap[k] = v
				}
			}
			output["mergedConfiguration"] = mergedMap
		} else {
			output["mergedConfiguration"] = merged
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

// configToMetadataEntry converts a DevContainerConfig to a metadata Entry
// for merging. When omitRemoteEnv is true, remoteEnv is excluded from the
// entry (used for image metadata labels with --omit-config-remote-env-from-metadata).
func configToMetadataEntry(cfg *config.DevContainerConfig, omitRemoteEnv ...bool) imagemeta.Entry {
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
	// label lossy vs the TS CLI (blocker B10).
	if len(cfg.ForwardPorts) > 0 {
		fp := make([]interface{}, len(cfg.ForwardPorts))
		for i, p := range cfg.ForwardPorts {
			fp[i] = p
		}
		entry.ForwardPorts = fp
	}
	if len(cfg.PortsAttributes) > 0 {
		pa := make(map[string]interface{}, len(cfg.PortsAttributes))
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
	shouldOmit := len(omitRemoteEnv) > 0 && omitRemoteEnv[0]
	if cfg.RemoteEnv != nil && !shouldOmit {
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

// envSliceToMap moved to helpers.go
