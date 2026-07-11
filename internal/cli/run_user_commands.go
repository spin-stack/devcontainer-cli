package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/core/errors"
	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
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
			return runUserCommands(&opts)
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
	f.StringVar(&opts.defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "")
	f.Bool("stop-for-personalization", false, "")
	f.Bool("skip-feature-auto-mapping", false, "")
	f.StringVar(&opts.containerSessionDataFolder, "container-session-data-folder", "", "")
	f.StringVar(&opts.secretsFile, "secrets-file", "", "")
	f.String("user-data-folder", "", "")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")

	addLogFileFlags(cmd)
	return cmd
}

func runUserCommands(opts *runUserCommandsOpts) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return writeValidationError(err.Error())
	}
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return writeValidationError(err.Error())
	}
	// 0.88: default --workspace-folder to cwd when no --container-id/--id-label/--workspace-folder.
	if opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.containerID == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}
	for _, v := range []struct {
		flag, val string
		choices   []string
	}{
		{"log-level", opts.logLevel, []string{"info", "debug", "trace"}},
		{"log-format", opts.logFormat, []string{"text", "json"}},
		{"default-user-env-probe", opts.defaultUserEnvProbe, []string{"none", "loginShell", "interactiveShell", "loginInteractiveShell"}},
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return writeValidationError(err.Error())
		}
	}
	if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
		return writeValidationError(err.Error())
	}

	logger := log.New(log.Options{
		Version:    cliVersion(),
		Level:      log.MapLogLevel(opts.logLevel),
		Format:     opts.logFormat,
		Writer:     os.Stderr,
		Dimensions: logDimensions(opts.terminalColumns, opts.terminalRows),
		Secrets:    secretValuesFromFile(opts.secretsFile),
	})

	var (
		loadResult *config.LoadResult
		loadErr    error
	)

	// Try to load config first — provides better error messages
	if opts.workspaceFolder != "" {
		ws := resolvePath(opts.workspaceFolder)
		loadResult, loadErr = config.LoadDevContainerConfig(ws, opts.configPath, opts.overrideConfig)
		if loadErr != nil {
			return writeErrorJSON(coreerrors.ToErrorOutput(&coreerrors.ContainerError{
				Description: loadErr.Error(),
			}))
		}
	}

	ctx := context.Background()
	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	// Find container
	containerID := opts.containerID
	if containerID == "" {
		labels := opts.idLabels
		if len(labels) == 0 && opts.workspaceFolder != "" {
			labels = []string{fmt.Sprintf("devcontainer.local_folder=%s", resolvePath(opts.workspaceFolder))}
		}
		if len(labels) > 0 {
			ids, _ := engine.ListContainers(ctx, true, labels)
			if len(ids) > 0 {
				containerID = ids[0]
			}
		}
	}

	if containerID == "" {
		return writeErrorResult("Dev container not found.")
	}

	// Load config from --workspace-folder or --config
	var cfg *config.DevContainerConfig
	if loadResult != nil {
		cfg = loadResult.Config
	} else if opts.workspaceFolder != "" || opts.configPath != "" {
		ws := "."
		if opts.workspaceFolder != "" {
			ws = resolvePath(opts.workspaceFolder)
		}
		cp := opts.configPath
		if cp != "" && !filepath.IsAbs(cp) {
			cwd, _ := os.Getwd()
			cp = filepath.Join(cwd, cp)
		}
		loadResult, loadErr := config.LoadDevContainerConfig(ws, cp, opts.overrideConfig)
		if loadErr == nil {
			cfg = loadResult.Config
		}
	}

	// Read metadata from container
	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return writeErrorResult(fmt.Sprintf("Failed to inspect container: %v", err))
	}

	// Build merged config. With a config loaded, read the image's metadata
	// (features are baked into the image) + the config entry — reading the
	// container label (which now carries the config metadata) and re-adding the
	// config entry would double the lifecycle commands (B14). Without a config
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

	// Resolve remoteUser (metadata, overridden by config) so the shell server and
	// userEnvProbe run as that user, matching the TS CLI.
	remoteUser := merged.RemoteUser
	if cfg != nil && cfg.RemoteUser != "" {
		remoteUser = cfg.RemoteUser
	}

	// Build merged remote env: probe → secrets → CLI --remote-env → config remoteEnv
	probeStrategy := lifecycle.UserEnvProbeStrategy(opts.defaultUserEnvProbe)
	if cfg != nil && cfg.UserEnvProbe != "" {
		probeStrategy = lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}

	var mergedRemoteEnv []string
	if probeStrategy != lifecycle.ProbeNone {
		probeServer, probeErr := lifecycle.NewShellServer(opts.dockerPath, containerID, remoteUser, logger)
		if probeErr == nil {
			probedEnv, _ := lifecycle.ProbeRemoteEnv(logger, probeServer, probeStrategy, remoteUser, opts.containerSessionDataFolder)
			probeServer.Close()
			for k, v := range probedEnv {
				mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}
	if opts.secretsFile != "" {
		if secretEnvs, secErr := readSecretsFile(opts.secretsFile); secErr == nil {
			mergedRemoteEnv = append(mergedRemoteEnv, secretEnvs...)
		}
	}
	mergedRemoteEnv = append(mergedRemoteEnv, opts.remoteEnvs...)
	if cfg != nil {
		for k, v := range cfg.RemoteEnv {
			if v != nil {
				mergedRemoteEnv = append(mergedRemoteEnv, fmt.Sprintf("%s=%s", k, *v))
			}
		}
	}

	shellServer, err := lifecycle.NewShellServer(opts.dockerPath, containerID, remoteUser, logger, mergedRemoteEnv...)
	if err != nil {
		return writeErrorResult(fmt.Sprintf("Failed to start shell server: %v", err))
	}
	defer shellServer.Close()

	workDir := ""
	if loadResult != nil && loadResult.WorkspaceConfig != nil {
		workDir = loadResult.WorkspaceConfig.WorkspaceFolder
	}
	executor := &lifecycle.ShellExecutor{Server: shellServer, Log: logger, WorkDir: workDir}

	// Install dotfiles
	if opts.dotfilesRepo != "" {
		lifecycle.InstallDotfiles(logger, executor, lifecycle.DotfilesConfig{
			Repository:     opts.dotfilesRepo,
			InstallCommand: opts.dotfilesCommand,
			TargetPath:     opts.dotfilesTarget,
		})
	}

	// Run lifecycle hooks
	hookErr := lifecycle.RunHooks(logger, executor, merged, lifecycle.RunOptions{
		SkipNonBlocking: opts.skipNonBlocking,
		SkipPostAttach:  opts.skipPostAttach,
		Prebuild:        opts.prebuild,
	})

	if hookErr != nil {
		var hook *lifecycle.HookError
		var cmdErr *lifecycle.CommandError
		if errors.As(hookErr, &hook) && errors.As(hookErr, &cmdErr) {
			description := fmt.Sprintf("%s from devcontainer.json failed.", hook.Phase)
			return writeErrorJSON(coreerrors.ErrorOutput{
				Outcome:     "error",
				Message:     cmdErr.Error(),
				Description: description,
			})
		}
		return writeErrorJSON(coreerrors.ToErrorOutput(&coreerrors.ContainerError{
			Description: fmt.Sprintf("An error occurred running user commands: %v", hookErr),
		}))
	}

	return writeSuccessJSON(map[string]interface{}{
		"outcome": "success",
		"result":  "done",
	})
}
