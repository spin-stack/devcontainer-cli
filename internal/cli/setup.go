package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/moby/api/types/container"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

type setUpOpts struct {
	dockerPath                 string
	containerID                string
	configPath                 string
	logLevel                   string
	logFormat                  string
	logFile                    string
	terminalLogFile            string
	skipPostCreate             bool
	skipNonBlocking            bool
	dotfilesRepo               string
	dotfilesCommand            string
	dotfilesTarget             string
	includeConfig              bool
	includeMerged              bool
	remoteEnvs                 []string
	defaultUserEnvProbe        string
	containerSessionDataFolder string
	terminalColumns            int
	terminalRows               int
}

func newSetUpCmd() *cobra.Command {
	var opts setUpOpts

	cmd := &cobra.Command{
		Use:   "set-up",
		Short: "Set up an existing container as a dev container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetUp(cmd.Context(), outputFor(cmd), &opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	f.StringVar(&opts.containerID, "container-id", "", "Id of the container.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")
	f.BoolVar(&opts.skipPostCreate, "skip-post-create", false, "Skip post-create commands.")
	f.BoolVar(&opts.skipNonBlocking, "skip-non-blocking-commands", false, "Stop after waitFor.")
	f.StringVar(&opts.dotfilesRepo, "dotfiles-repository", "", "Dotfiles repo.")
	f.StringVar(&opts.dotfilesCommand, "dotfiles-install-command", "", "Dotfiles install command.")
	f.StringVar(&opts.dotfilesTarget, "dotfiles-target-path", "~/dotfiles", "Dotfiles target.")
	f.BoolVar(&opts.includeConfig, "include-configuration", false, "Include config.")
	f.BoolVar(&opts.includeMerged, "include-merged-configuration", false, "Include merged config.")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.StringVar(&opts.defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "")
	f.String("user-data-folder", "", "")
	f.StringArrayVar(&opts.remoteEnvs, "remote-env", nil, "Remote env vars.")
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

// runSetUp validates the flags, opens the log and finds the container, then
// hands it to setUpContainer.
func runSetUp(ctx context.Context, out Output, opts *setUpOpts) error {
	if opts.containerID == "" {
		return writeValidationError(out, "Missing required argument: --container-id")
	}
	if err := validateEnums(
		enumFlag{"log-level", opts.logLevel, logLevels},
		enumFlag{"log-format", opts.logFormat, logFormats},
		enumFlag{"default-user-env-probe", opts.defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
	); err != nil {
		return writeValidationError(out, err.Error())
	}
	if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
		return writeValidationError(out, err.Error())
	}
	// TS parity: set-up rejects malformed --remote-env at parse time like
	// its siblings (exec/up/run-user-commands); Go was missing this.
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return writeValidationError(out, err.Error())
	}

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
	})

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	inspect, err := engine.InspectContainer(ctx, opts.containerID)
	if err != nil {
		return writeErrorResult(out, "Dev container not found.")
	}
	return setUpContainer(ctx, out, engine, logger, opts, inspect)
}

// setUpContainer runs the lifecycle hooks in the container and writes the
// result.
//
// mutate-lane: TestParityMatrix - set-up.container-config-success,
// set-up.container-metadata-success and set-up.after-failed-post-create-success
// set up a real container through it.
func setUpContainer(ctx context.Context, out Output, engine *docker.EngineClient, logger log.Logger, opts *setUpOpts, inspect container.InspectResponse) error {
	containerID := opts.containerID
	cfg := setUpConfig(opts.configPath)
	merged := setUpMerged(inspect.Config.Labels, cfg, logger)

	var cfgProbe string
	var cfgRemoteEnv map[string]*string
	if cfg != nil {
		cfgProbe, cfgRemoteEnv = cfg.UserEnvProbe, cfg.RemoteEnv
	}
	// The shell server and userEnvProbe run as the merged remoteUser (the
	// config's wins), matching the TS CLI. Remote env: probe → CLI --remote-env
	// → config remoteEnv.
	probeStrategy := lifecycle.UserEnvProbeStrategy(cmp.Or(cfgProbe, opts.defaultUserEnvProbe))
	probed := lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, merged.RemoteUser, probeStrategy, opts.containerSessionDataFolder)
	shellServer := lifecycle.NewShellServer(ctx, engine, containerID, merged.RemoteUser, hookRemoteEnv(probed, opts.remoteEnvs, cfgRemoteEnv)...)
	defer shellServer.Close()

	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger}

	if !opts.skipPostCreate {
		applyLifecycleMarkers(shellServer, merged, inspect, false)
		hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
			SkipNonBlocking: opts.skipNonBlocking,
			AfterPostCreate: dotfilesAfterPostCreate(logger, executor, lifecycle.DotfilesConfig{
				Repository:     opts.dotfilesRepo,
				InstallCommand: opts.dotfilesCommand,
				TargetPath:     opts.dotfilesTarget,
			}),
		})
		if hookErr != nil {
			// TS set-up reports only the message and description.
			return writeErrorJSON(out, coreerrors.ToErrorOutput(hookContainerError(hookErr, "", "An error occurred running user commands in the container.")))
		}
	}

	result, err := setUpResult(cfg, merged, config.EnvMap(inspect.Config.Env), opts.includeConfig, opts.includeMerged)
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	return writeSuccessJSON(out, result)
}

// setUpConfig is the --config file set-up merges over the container's
// metadata: nil when no --config is given or it cannot be loaded.
func setUpConfig(configPath string) *config.DevContainer {
	if configPath == "" {
		return nil
	}
	loadResult, err := config.LoadDevContainerConfig(".", resolvePath(configPath), "")
	if err != nil {
		return nil
	}
	return loadResult.Config
}

// setUpMerged is set-up's merged configuration: the container's metadata label
// entries, then the config (when there is one) as the last entry.
func setUpMerged(labels map[string]string, cfg *config.DevContainer, logger log.Logger) *imagemeta.MergedConfig {
	entries := imagemeta.ReadMetadataFromLabels(labels, logger)
	if cfg != nil {
		entries = append(entries, configToMetadataEntry(cfg))
	}
	return imagemeta.MergeConfiguration(entries)
}

// setUpResult is set-up's success result: configuration
// (--include-configuration; {} without a config) and mergedConfiguration
// (--include-merged-configuration), with ${containerEnv:…} resolved from the
// container's environment and configFilePath as a file URI, as TS reports them.
func setUpResult(cfg *config.DevContainer, merged *imagemeta.MergedConfig, containerEnv map[string]string, includeConfig, includeMerged bool) (map[string]any, error) {
	result := map[string]any{"outcome": "success"}

	if includeConfig {
		if cfg != nil {
			// Substitute containerEnv in config
			cfgJSON, _ := json.Marshal(cfg)
			var cfgGeneric any
			json.Unmarshal(cfgJSON, &cfgGeneric)
			substituted, subErr := resolveContainerVariables(containerEnv, cfgGeneric)
			if subErr != nil {
				return nil, fmt.Errorf("resolve configuration container variables: %w", subErr)
			}
			// Clean null values and internal fields
			if subMap, ok := substituted.(map[string]any); ok {
				for k, v := range subMap {
					if v == nil {
						delete(subMap, k)
					}
				}
				// Convert configFilePath to URI format (matches TS)
				if cfp, ok := subMap["configFilePath"].(string); ok && cfp != "" {
					subMap["configFilePath"] = map[string]any{
						"$mid":   1,
						"fsPath": cfp,
						"path":   cfp,
						"scheme": "file",
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
		} else {
			// No config file → empty object (TS behavior)
			result["configuration"] = map[string]any{}
		}
	}

	if includeMerged {
		// Copy configFilePath URI to mergedConfiguration (matches TS)
		if cfgResult, ok := result["configuration"].(map[string]any); ok {
			if cfpURI, ok := cfgResult["configFilePath"]; ok {
				merged.ConfigFilePath = cfpURI
			}
		}
		mergedJSON, _ := json.Marshal(merged)
		var mergedGeneric any
		json.Unmarshal(mergedJSON, &mergedGeneric)
		substituted, subErr := resolveContainerVariables(containerEnv, mergedGeneric)
		if subErr != nil {
			return nil, fmt.Errorf("resolve merged container variables: %w", subErr)
		}
		result["mergedConfiguration"] = substituted
	}
	return result, nil
}
