package cli

import (
	"context"
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

type runUserCommandsOpts struct {
	workspaceFolder            string
	configPath                 string
	overrideConfig             string
	dockerPath                 string
	containerID                string
	idLabels                   []string
	logLevel                   string
	logFormat                  string
	logFile                    string
	terminalLogFile            string
	skipNonBlocking            bool
	skipPostAttach             bool
	prebuild                   bool
	remoteEnvs                 []string
	dotfilesRepo               string
	dotfilesCommand            string
	dotfilesTarget             string
	defaultUserEnvProbe        string
	containerSessionDataFolder string
	secretsFile                string
	terminalColumns            givenInt
	terminalRows               givenInt
}

func newRunUserCommandsCmd() *cobra.Command {
	var opts runUserCommandsOpts

	cmd := &cobra.Command{
		Use:   "run-user-commands",
		Short: "Run user commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUserCommands(cmd.Context(), outputFor(cmd), &opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.overrideConfig, "override-config", "", "Override config path.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	f.StringVar(&opts.containerID, "container-id", "", "Container ID.")
	f.StringArrayVar(&opts.idLabels, "id-label", nil, "Id label(s).")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")
	f.BoolVar(&opts.skipNonBlocking, "skip-non-blocking-commands", false, "Stop after waitFor.")
	f.BoolVar(&opts.skipPostAttach, "skip-post-attach", false, "Skip postAttachCommand.")
	f.BoolVar(&opts.prebuild, "prebuild", false, "Stop after updateContentCommand.")
	f.StringArrayVar(&opts.remoteEnvs, "remote-env", nil, "Remote env vars.")
	f.StringVar(&opts.dotfilesRepo, "dotfiles-repository", "", "Dotfiles repo URL.")
	f.StringVar(&opts.dotfilesCommand, "dotfiles-install-command", "", "Dotfiles install command.")
	f.StringVar(&opts.dotfilesTarget, "dotfiles-target-path", "~/dotfiles", "Dotfiles target.")

	f.String("docker-compose-path", "", "")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.Bool("mount-workspace-git-root", true, "")
	f.Bool("mount-git-worktree-common-dir", false, "")
	f.StringVar(&opts.defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "")
	f.Bool("stop-for-personalization", false, "")
	f.Bool("skip-feature-auto-mapping", false, "")
	_ = f.MarkHidden("skip-feature-auto-mapping")
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "")
	f.String("user-data-folder", "", "")
	f.Var(&opts.terminalColumns, "terminal-columns", "")
	f.Var(&opts.terminalRows, "terminal-rows", "")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

func runUserCommands(ctx context.Context, out Output, opts *runUserCommandsOpts) error {
	if err := prepareRunUserCommandsOpts(opts); err != nil {
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
		Secrets: secretValuesFromFile(opts.secretsFile),
	})

	var loadResult *config.LoadResult
	if opts.workspaceFolder != "" {
		var loadErr error
		loadResult, loadErr = config.LoadDevContainerConfig(resolvePath(opts.workspaceFolder), opts.configPath, opts.overrideConfig)
		if loadErr != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: loadErr.Error(),
			}))
		}
	}

	return runUserCommandsInContainer(ctx, out, opts, logger, loadResult)
}

func prepareRunUserCommandsOpts(opts *runUserCommandsOpts) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return err
	}
	if opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.containerID == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}
	if err := validateEnums(
		enumFlag{"log-level", opts.logLevel, logLevels},
		enumFlag{"log-format", opts.logFormat, logFormats},
		enumFlag{"default-user-env-probe", opts.defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
	); err != nil {
		return err
	}
	return validateTerminalImplications(opts.terminalColumns, opts.terminalRows)
}

// mutate-lane: TestParityMatrix (run-user-commands.container-config-success,
// run-user-commands.workspace-image-success,
// run-user-commands.skip-post-attach-success,
// run-user-commands.workspace-secrets-success,
// run-user-commands.workspace-subfolder-config-success,
// run-user-commands.container-metadata-success,
// run-user-commands.hook-failure-error); TestE2E_CABundle runs it on a
// container up created with --ca-bundle.
func runUserCommandsInContainer(ctx context.Context, out Output, opts *runUserCommandsOpts, logger log.Logger, loadResult *config.LoadResult) error {
	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	containerID := opts.containerID
	if containerID == "" {
		lookupConfigFile := ""
		if loadResult != nil {
			lookupConfigFile = loadResult.Config.ConfigFilePath
		}
		var lookupErr error
		containerID, lookupErr = resolveContainerID(ctx, engine, opts.workspaceFolder, lookupConfigFile, opts.idLabels)
		if lookupErr != nil {
			return writeErrorJSON(out, coreerrors.ToErrorOutput(&coreerrors.ContainerError{Description: "An error occurred running user commands in the container.", OriginalError: lookupErr}))
		}
	}
	if containerID == "" {
		return writeErrorResult(out, "Dev container not found.")
	}

	var cfg *config.DevContainer
	if loadResult != nil {
		cfg = loadResult.Config
	} else {
		cfg = configFromFlag(opts.configPath, opts.overrideConfig)
	}

	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Failed to inspect container: %v", err))
	}
	hostSub, configFile := config.EnvHostSubContext(), ""
	if loadResult != nil {
		hostSub, configFile = loadResult.HostSub, loadResult.Config.ConfigFilePath
	}
	var containerLabels map[string]string
	if inspect.Config != nil {
		containerLabels = inspect.Config.Labels
	}
	idLabels := containerIDLabels(opts.idLabels, containerLabels, resolvePath(opts.workspaceFolder), configFile)
	allEntries, err := imageMetadataFromContainer(containerLabels, cfg, nil, idLabels, hostMetadataSubstitution(hostSub, idLabels), logger)
	if err != nil {
		return writeErrorResult(out, err.Error())
	}
	merged := imagemeta.MergeConfiguration(allEntries)
	var rawConfig any = cfg
	if loadResult != nil {
		rawConfig = loadResult.Raw
	}
	if cfg != nil {
		configFile = cfg.ConfigFilePath
	}
	var containerEnv map[string]string
	if inspect.Config != nil {
		containerEnv = config.EnvMap(inspect.Config.Env)
	}
	if err := checkContainerSubstitution(containerEnv, configFile, rawConfig, merged); err != nil {
		return writeErrorJSON(out, coreerrors.ToErrorOutput(err))
	}

	remoteUser := lifecycleRemoteUser(merged, cfg)
	probed := lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, remoteUser, lifecycleUserEnvProbe(opts.defaultUserEnvProbe, cfg), opts.containerSessionDataFolder)
	remoteEnv := lifecycleRemoteEnv(probed, buildSecretsFromFile(opts.secretsFile), opts.remoteEnvs, cfg)

	shellServer := lifecycle.NewShellServer(ctx, engine, containerID, remoteUser, remoteEnv...)
	defer shellServer.Close()

	workDir := ""
	if loadResult != nil && loadResult.WorkspaceConfig != nil {
		workDir = loadResult.WorkspaceConfig.WorkspaceFolder
	}
	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger, WorkDir: workDir}

	applyLifecycleMarkers(shellServer, merged, inspect, opts.prebuild)
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
		return writeErrorJSON(out, coreerrors.ToErrorOutput(hookContainerError(hookErr, "", "An error occurred running user commands in the container.")))
	}

	return writeSuccessJSON(out, map[string]any{
		"outcome": "success",
		"result":  "done",
	}, runUserCommandsResultKeys...)
}

func configFromFlag(configPath, overrideConfig string) *config.DevContainer {
	if configPath == "" {
		return nil
	}
	loadResult, err := config.LoadDevContainerConfig(".", resolvePath(configPath), overrideConfig)
	if err != nil {
		return nil
	}
	return loadResult.Config
}

func lifecycleRemoteUser(merged *imagemeta.MergedConfig, cfg *config.DevContainer) string {
	if cfg != nil && cfg.RemoteUser != "" {
		return cfg.RemoteUser
	}
	return merged.RemoteUser
}

func lifecycleUserEnvProbe(defaultProbe string, cfg *config.DevContainer) lifecycle.UserEnvProbeStrategy {
	if cfg != nil && cfg.UserEnvProbe != "" {
		return lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}
	return lifecycle.UserEnvProbeStrategy(defaultProbe)
}

func lifecycleRemoteEnv(probed map[string]string, secrets, remoteEnvs []string, cfg *config.DevContainer) []string {
	var env []string
	for k, v := range probed {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = append(env, secrets...)
	env = append(env, remoteEnvs...)
	if cfg != nil {
		for k, v := range cfg.RemoteEnv {
			if v != nil {
				env = append(env, fmt.Sprintf("%s=%s", k, *v))
			}
		}
	}
	return env
}
