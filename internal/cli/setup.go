package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"

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
	terminalColumns            givenInt
	terminalRows               givenInt
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
	f.Var(&opts.terminalColumns, "terminal-columns", "")
	f.Var(&opts.terminalRows, "terminal-rows", "")

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

	// TS doSetUp reads the --config file before it looks for the container.
	loaded, err := setUpConfig(opts.configPath)
	if err != nil {
		return writeErrorJSON(out, setUpErrorOutput(err))
	}

	inspect, err := engine.InspectContainer(ctx, opts.containerID)
	if err != nil {
		return writeErrorResult(out, "Dev container not found.")
	}
	return setUpContainer(ctx, out, engine, logger, opts, loaded, inspect)
}

// setUpErrorOutput is TS doSetUp's error result: a ContainerError as it is
// (a NotObjectError is one in TS), any other error described as "An error
// occurred running user commands in the container.".
func setUpErrorOutput(err error) coreerrors.ErrorOutput {
	if _, ok := errors.AsType[*config.NotObjectError](err); ok {
		return coreerrors.ToErrorOutput(&coreerrors.ContainerError{Description: err.Error()})
	}
	if ce, ok := coreerrors.AsContainerError(err); ok {
		return coreerrors.ToErrorOutput(ce)
	}
	return coreerrors.ToErrorOutput(&coreerrors.ContainerError{Description: "An error occurred running user commands in the container.", OriginalError: err})
}

// setUpContainer runs the lifecycle hooks in the container and writes the
// result.
//
// mutate-lane: TestParityMatrix - set-up.container-config-success,
// set-up.container-metadata-success and set-up.after-failed-post-create-success
// set up a real container through it.
func setUpContainer(ctx context.Context, out Output, engine *docker.EngineClient, logger log.Logger, opts *setUpOpts, loaded *config.LoadResult, inspect container.InspectResponse) error {
	containerID := opts.containerID
	var cfg *config.DevContainer
	if loaded != nil {
		cfg = loaded.Config
	}
	entries := setUpEntries(inspect.Config.Labels, cfg, logger)
	merged := imagemeta.MergeConfiguration(entries)
	result, err := setUpResult(loaded, entries, inspect.Config, opts.includeConfig, opts.includeMerged)
	if err != nil {
		return writeErrorJSON(out, setUpErrorOutput(err))
	}

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

	orderConfigurations(result, loaded)
	return writeSuccessJSON(out, result, setUpResultKeys...)
}

// setUpConfig loads the --config file set-up merges over the container's
// metadata (nil without --config) as TS doSetUp reads it: with no workspace,
// so ${localWorkspaceFolder} and its basename stay as written, and so does
// ${containerWorkspaceFolder} unless the config sets workspaceFolder. A file
// that does not exist is TS's "Dev container config (...) not found.".
func setUpConfig(configPath string) (*config.LoadResult, error) {
	if configPath == "" {
		return nil, nil
	}
	path := resolvePath(configPath)
	loaded, err := config.LoadDevContainerConfig("", path, "")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &coreerrors.ContainerError{Description: fmt.Sprintf("Dev container config (%s) not found.", path)}
	}
	return loaded, err
}

// setUpEntries are the metadata entries set-up merges: the container's
// metadata label entries, then the config (when there is one) as the last
// entry.
func setUpEntries(labels map[string]string, cfg *config.DevContainer, logger log.Logger) []imagemeta.Entry {
	entries := imagemeta.ReadMetadataFromLabels(labels, logger)
	if cfg != nil {
		entries = append(entries, configToMetadataEntry(cfg))
	}
	return entries
}

// setUpResult is set-up's success result as TS doSetUp reports it:
// configuration (--include-configuration) is the loaded configuration ({}
// without one) and mergedConfiguration (--include-merged-configuration) the
// merge of entries over it, both with ${containerEnv:...} resolved from the
// container's environment. configFilePath is the file URI, which TS never
// substitutes.
//
// TS substitutes both before it runs the lifecycle hooks, whether or not they
// are reported, so set-up computes the result first: a variable that cannot be
// resolved is the ContainerError it fails with, naming the configuration's
// file.
func setUpResult(loaded *config.LoadResult, entries []imagemeta.Entry, containerCfg *container.Config, includeConfig, includeMerged bool) (map[string]any, error) {
	cfgMap := map[string]any{}
	var configFileURI any
	configFile := ""
	if loaded != nil {
		configFile = loaded.Config.ConfigFilePath
		cfgMap = configurationOutput(loaded, &readConfigOpts{configPath: configFile})
		configFileURI = cfgMap["configFilePath"]
		delete(cfgMap, "configFilePath")
	}
	substituted, err := substituteContainerEnv(containerCfg, cfgMap, configFile)
	if err != nil {
		return nil, err
	}
	if configFileURI != nil {
		substituted["configFilePath"] = configFileURI
	}
	merged, err := mergedConfigurationOutput(entries, containerCfg, substituted, configFile)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"outcome": "success"}
	if includeConfig {
		result["configuration"] = substituted
	}
	if includeMerged {
		result["mergedConfiguration"] = merged
	}
	return result, nil
}
