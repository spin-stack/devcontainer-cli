package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

func newSetUpCmd() *cobra.Command {
	var (
		dockerPath                 string
		containerID                string
		configPath                 string
		logLevel                   string
		logFormat                  string
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
	)

	cmd := &cobra.Command{
		Use:   "set-up",
		Short: "Set up an existing container as a dev container",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := outputFor(cmd)
			if containerID == "" {
				return writeValidationError(out, "Missing required argument: --container-id")
			}
			for _, v := range []struct {
				flag, val string
				choices   []string
			}{
				{"log-level", logLevel, []string{"info", "debug", "trace"}},
				{"log-format", logFormat, []string{"text", "json"}},
				{"default-user-env-probe", defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
			} {
				if err := validateEnum(v.flag, v.val, v.choices); err != nil {
					return writeValidationError(out, err.Error())
				}
			}
			if err := validateTerminalImplications(terminalColumns, terminalRows); err != nil {
				return writeValidationError(out, err.Error())
			}

			logger := log.New(log.Options{
				Version:    cliVersion(),
				Level:      log.MapLogLevel(logLevel),
				Format:     logFormat,
				Writer:     os.Stderr,
				Dimensions: logDimensions(terminalColumns, terminalRows),
			})

			ctx := cmd.Context()
			engine, err := docker.NewEngineClient(logger)
			if err != nil {
				return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
			}
			defer engine.Close()

			inspect, err := engine.InspectContainer(ctx, containerID)
			if err != nil {
				return writeErrorResult(out, "Dev container not found.")
			}

			// Get containerEnv from running container
			containerEnv := envSliceToMap(inspect.Config.Env)

			// Load config if provided
			var cfg *config.DevContainerConfig
			if configPath != "" {
				loadResult, loadErr := config.LoadDevContainerConfig(".", resolvePath(configPath), "")
				if loadErr == nil {
					cfg = loadResult.Config
				}
			}

			// Read metadata from container labels
			entries := imagemeta.ReadMetadataFromLabels(inspect.Config.Labels, logger)

			// Build merged config: metadata entries + config as last entry
			allEntries := make([]imagemeta.Entry, len(entries))
			copy(allEntries, entries)
			if cfg != nil {
				allEntries = append(allEntries, configToMetadataEntry(cfg))
			}
			merged := imagemeta.MergeConfiguration(allEntries)

			// Resolve remoteUser (metadata, overridden by config) so the shell server
			// and userEnvProbe run as that user, matching the TS CLI.
			remoteUser := merged.RemoteUser
			if cfg != nil && cfg.RemoteUser != "" {
				remoteUser = cfg.RemoteUser
			}

			// Build merged remote env: probe → CLI --remote-env → config remoteEnv
			probeStrategy := lifecycle.UserEnvProbeStrategy(defaultUserEnvProbe)
			if cfg != nil && cfg.UserEnvProbe != "" {
				probeStrategy = lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
			}

			var mergedRemoteEnv []string
			if probeStrategy != lifecycle.ProbeNone {
				probeServer, probeErr := lifecycle.NewShellServer(dockerPath, containerID, remoteUser, logger)
				if probeErr == nil {
					probedEnv, _ := lifecycle.ProbeRemoteEnv(logger, probeServer, probeStrategy, remoteUser, containerSessionDataFolder)
					probeServer.Close()
					for k, v := range probedEnv {
						mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, v))
					}
				}
			}
			mergedRemoteEnv = append(mergedRemoteEnv, remoteEnvs...)
			if cfg != nil {
				for k, v := range cfg.RemoteEnv {
					if v != nil {
						mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, *v))
					}
				}
			}

			shellServer, err := lifecycle.NewShellServer(dockerPath, containerID, remoteUser, logger, mergedRemoteEnv...)
			if err != nil {
				return writeErrorResult(out, fmt.Sprintf("Failed to start shell: %v", err))
			}
			defer shellServer.Close()

			executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger}

			if dotfilesRepo != "" {
				lifecycle.InstallDotfiles(logger, executor, lifecycle.DotfilesConfig{
					Repository:     dotfilesRepo,
					InstallCommand: dotfilesCommand,
					TargetPath:     dotfilesTarget,
				})
			}

			if !skipPostCreate {
				hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
					SkipNonBlocking: skipNonBlocking,
				})
				if hookErr != nil {
					return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
						Description: fmt.Sprintf("An error occurred running user commands: %v", hookErr),
					}))
				}
			}

			// Build result with containerEnv substitution
			result := map[string]interface{}{"outcome": "success"}

			if includeConfig {
				if cfg != nil {
					// Substitute containerEnv in config
					cfgJSON, _ := json.Marshal(cfg)
					var cfgGeneric interface{}
					json.Unmarshal(cfgJSON, &cfgGeneric)
					substituted := config.SubstituteContainer("linux", containerEnv, cfgGeneric)
					// Clean null values and internal fields
					if subMap, ok := substituted.(map[string]interface{}); ok {
						for k, v := range subMap {
							if v == nil {
								delete(subMap, k)
							}
						}
						// Convert configFilePath to URI format (matches TS)
						if cfp, ok := subMap["configFilePath"].(string); ok && cfp != "" {
							subMap["configFilePath"] = map[string]interface{}{
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
					result["configuration"] = map[string]interface{}{}
				}
			}

			if includeMerged {
				// Copy configFilePath URI to mergedConfiguration (matches TS)
				if cfgResult, ok := result["configuration"].(map[string]interface{}); ok {
					if cfpURI, ok := cfgResult["configFilePath"]; ok {
						merged.ConfigFilePath = cfpURI
					}
				}
				mergedJSON, _ := json.Marshal(merged)
				var mergedGeneric interface{}
				json.Unmarshal(mergedJSON, &mergedGeneric)
				substituted := config.SubstituteContainer("linux", containerEnv, mergedGeneric)
				result["mergedConfiguration"] = substituted
			}

			return writeSuccessJSON(out, result)
		},
	}

	f := cmd.Flags()
	f.StringVar(&dockerPath, "docker-path", "", "Docker CLI path.")
	f.StringVar(&containerID, "container-id", "", "Id of the container.")
	f.StringVar(&configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&logLevel, "log-level", "info", "Log level.")
	f.StringVar(&logFormat, "log-format", "text", "Log format.")
	f.BoolVar(&skipPostCreate, "skip-post-create", false, "Skip post-create commands.")
	f.BoolVar(&skipNonBlocking, "skip-non-blocking-commands", false, "Stop after waitFor.")
	f.StringVar(&dotfilesRepo, "dotfiles-repository", "", "Dotfiles repo.")
	f.StringVar(&dotfilesCommand, "dotfiles-install-command", "", "Dotfiles install command.")
	f.StringVar(&dotfilesTarget, "dotfiles-target-path", "~/dotfiles", "Dotfiles target.")
	f.BoolVar(&includeConfig, "include-configuration", false, "Include config.")
	f.BoolVar(&includeMerged, "include-merged-configuration", false, "Include merged config.")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.StringVar(&defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "")
	f.String("user-data-folder", "", "")
	f.StringArrayVar(&remoteEnvs, "remote-env", nil, "Remote env vars.")
	f.StringVar(&containerSessionDataFolder, "container-session-data-folder", "", "")
	f.IntVar(&terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&terminalRows, "terminal-rows", 0, "")

	addLogFileFlags(cmd)
	return cmd
}
