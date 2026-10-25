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
	terminalColumns            int
	terminalRows               int
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
	_ = f.MarkHidden("skip-feature-auto-mapping") // hidden testing flag (TS parity)
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "")
	f.String("user-data-folder", "", "")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")

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

	// Try to load config first — provides better error messages
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

// prepareRunUserCommandsOpts validates the run-user-commands flags and defaults
// --workspace-folder to the current directory when neither it, --container-id
// nor --id-label says which container to use.
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

// runUserCommandsInContainer finds the dev container and runs the lifecycle
// commands in it. loadResult is the config --workspace-folder loaded, or nil.
//
// mutate-lane: TestParityMatrix (run-user-commands.container-config-success,
// run-user-commands.workspace-image-success,
// run-user-commands.skip-post-attach-success,
// run-user-commands.workspace-secrets-success,
// run-user-commands.workspace-subfolder-config-success,
// run-user-commands.container-metadata-success,
// run-user-commands.hook-failure-error).
func runUserCommandsInContainer(ctx context.Context, out Output, opts *runUserCommandsOpts, logger log.Logger, loadResult *config.LoadResult) error {
	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	containerID := opts.containerID
	if containerID == "" {
		containerID = resolveContainerID(ctx, engine, opts.workspaceFolder, "", opts.idLabels)
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

	// Read metadata from container
	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Failed to inspect container: %v", err))
	}
	// Build merged config. With a config loaded, read the image's metadata
	// (features are baked into the image) + the config entry — reading the
	// container label (which now carries the config metadata) and re-adding the
	// config entry would double the lifecycle commands. Without a config
	// (container-id path) the container metadata is authoritative.
	var allEntries []imagemeta.Entry
	if cfg != nil {
		if inspect.Image != "" {
			if img, imgErr := engine.InspectImage(ctx, inspect.Image); imgErr == nil && img.Config != nil {
				allEntries = imagemeta.ReadMetadataFromLabels(img.Config.Labels, logger)
			}
		}
		allEntries = append(allEntries, configToMetadataEntry(cfg))
	} else {
		allEntries = imagemeta.ReadMetadataFromLabels(inspect.Config.Labels, logger)
	}
	merged := imagemeta.MergeConfiguration(allEntries)

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
		// TS run-user-commands reports only the message and description.
		return writeErrorJSON(out, coreerrors.ToErrorOutput(hookContainerError(hookErr, "", "An error occurred running user commands in the container.")))
	}

	return writeSuccessJSON(out, map[string]any{
		"outcome": "success",
		"result":  "done",
	})
}

// configFromFlag is the config --config names when no --workspace-folder loaded
// one (the --container-id path); a relative path is taken from the current
// directory. A config that does not load leaves the container's metadata as the
// only source, so it yields nil.
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

// lifecycleRemoteUser is the user the shell server and userEnvProbe run as: the
// metadata's remoteUser, overridden by the config's, matching the TS CLI.
func lifecycleRemoteUser(merged *imagemeta.MergedConfig, cfg *config.DevContainer) string {
	if cfg != nil && cfg.RemoteUser != "" {
		return cfg.RemoteUser
	}
	return merged.RemoteUser
}

// lifecycleUserEnvProbe is the config's userEnvProbe, or the
// --default-user-env-probe value when the config sets none.
func lifecycleUserEnvProbe(defaultProbe string, cfg *config.DevContainer) lifecycle.UserEnvProbeStrategy {
	if cfg != nil && cfg.UserEnvProbe != "" {
		return lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}
	return lifecycle.UserEnvProbeStrategy(defaultProbe)
}

// lifecycleRemoteEnv is the KEY=VALUE environment the lifecycle commands get, in
// the order later entries win: the probed environment, the --secrets-file
// secrets, --remote-env, then the config's remoteEnv, whose null values are left
// out.
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
