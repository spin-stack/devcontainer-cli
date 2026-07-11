package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

type execOpts struct {
	workspaceFolder     string
	configPath          string
	overrideConfig      string
	dockerPath          string
	containerID         string
	idLabels            []string
	logLevel            string
	logFormat           string
	remoteEnvs          []string
	terminalColumns     int
	terminalRows        int
	defaultUserEnvProbe string
}

func newExecCmd() *cobra.Command {
	var opts execOpts

	cmd := &cobra.Command{
		Use:                "exec [cmd] [args...]",
		Short:              "Execute a command on a running dev container",
		DisableFlagParsing: true, // Manual parsing to support `exec docker --version`
		RunE: func(cmd *cobra.Command, rawArgs []string) error {
			// Split: flags (--workspace-folder etc.) go to Cobra, rest is the command
			flagArgs, cmdArgs := splitExecArgs(rawArgs)

			// `exec --help`/`-h` (before the command) prints usage and exits 0,
			// matching the TS CLI. Without this, DisableFlagParsing swallows the
			// flag and the empty command triggers a spurious "requires at least 1 arg".
			for _, a := range flagArgs {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}

			// Re-enable flag parsing for our flags only
			cmd.DisableFlagParsing = false
			if err := cmd.ParseFlags(flagArgs); err != nil {
				return err
			}

			opts.workspaceFolder, _ = cmd.Flags().GetString("workspace-folder")
			opts.configPath, _ = cmd.Flags().GetString("config")
			opts.overrideConfig, _ = cmd.Flags().GetString("override-config")
			opts.dockerPath, _ = cmd.Flags().GetString("docker-path")
			opts.containerID, _ = cmd.Flags().GetString("container-id")
			opts.idLabels, _ = cmd.Flags().GetStringArray("id-label")
			opts.logLevel, _ = cmd.Flags().GetString("log-level")
			opts.logFormat, _ = cmd.Flags().GetString("log-format")
			opts.remoteEnvs, _ = cmd.Flags().GetStringArray("remote-env")
			opts.defaultUserEnvProbe, _ = cmd.Flags().GetString("default-user-env-probe")
			opts.terminalColumns, _ = cmd.Flags().GetInt("terminal-columns")
			opts.terminalRows, _ = cmd.Flags().GetInt("terminal-rows")

			if len(cmdArgs) == 0 {
				return fmt.Errorf("exec requires at least 1 arg")
			}
			return runExec(cmd.Context(), &opts, cmdArgs)
		},
	}

	f := cmd.Flags()
	f.String("workspace-folder", "", "Workspace folder path.")
	f.String("config", "", "devcontainer.json path.")
	f.String("override-config", "", "Override config path.")
	f.String("docker-path", "", "Docker CLI path.")
	f.String("container-id", "", "Container ID.")
	f.StringArray("id-label", nil, "Id label(s).")
	f.String("log-level", "info", "Log level.")
	f.String("log-format", "text", "Log format.")
	f.StringArray("remote-env", nil, "Remote env vars.")
	f.String("docker-compose-path", "", "")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.Bool("mount-workspace-git-root", true, "")
	f.String("default-user-env-probe", "loginInteractiveShell", "")
	f.Bool("skip-feature-auto-mapping", false, "")
	_ = f.MarkHidden("skip-feature-auto-mapping") // hidden testing flag (TS parity)
	f.String("user-data-folder", "", "")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")

	addLogFileFlags(cmd)
	return cmd
}

func runExec(ctx context.Context, opts *execOpts, cmdArgs []string) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return err
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
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return err
		}
	}
	if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
		return err
	}

	logger := log.New(log.Options{
		Version:    cliVersion(),
		Level:      log.MapLogLevel(opts.logLevel),
		Format:     opts.logFormat,
		Writer:     os.Stderr,
		Dimensions: logDimensions(opts.terminalColumns, opts.terminalRows),
	})

	// 1. If workspace-folder provided without container-id, validate config exists early
	var loadResult *config.LoadResult
	if opts.workspaceFolder != "" {
		ws := resolvePath(opts.workspaceFolder)
		cp := opts.configPath
		if cp != "" && !filepath.IsAbs(cp) {
			cwd, _ := os.Getwd()
			cp = filepath.Join(cwd, cp)
		}
		var loadErr error
		loadResult, loadErr = config.LoadDevContainerConfig(ws, cp, "")
		if loadErr != nil && opts.containerID == "" {
			return loadErr
		}
	}

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return fmt.Errorf("Docker engine: %w", err)
	}
	defer engine.Close()

	// 2. Find the container
	containerID := opts.containerID
	if containerID == "" {
		containerID = findContainerByOpts(ctx, engine, opts, logger)
	}

	if containerID == "" {
		return fmt.Errorf("Dev container not found.")
	}

	// 3. Inspect container
	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("Dev container not found.")
	}
	if inspect.State == nil || !inspect.State.Running {
		return fmt.Errorf("Dev container is not running.")
	}

	// 4. Get containerEnv from running container
	var containerEnv map[string]string
	if inspect.Config != nil {
		containerEnv = envSliceToMap(inspect.Config.Env)
	} else {
		containerEnv = map[string]string{}
	}

	// 5. Determine remoteUser: container default → metadata → config
	remoteUser := ""
	if inspect.Config != nil {
		remoteUser = inspect.Config.User
	}
	if remoteUser == "" {
		remoteUser = "root"
	}

	// 6. Read image metadata from container labels
	entries := imagemeta.ReadMetadataFromLabels(inspect.Config.Labels, logger)
	merged := imagemeta.MergeConfiguration(entries)

	// Use remoteUser from metadata (overrides container default)
	if merged.RemoteUser != "" {
		remoteUser = merged.RemoteUser
	}

	// 7. Probe user environment, then merge: probed → metadata → config → CLI (highest priority)
	probeStrategy := lifecycle.UserEnvProbeStrategy(opts.defaultUserEnvProbe)
	if loadResult != nil && loadResult.Config.UserEnvProbe != "" {
		probeStrategy = lifecycle.UserEnvProbeStrategy(loadResult.Config.UserEnvProbe)
	}

	resolvedRemoteEnv := map[string]string{}

	// Probed env (lowest priority)
	if probeStrategy != lifecycle.ProbeNone {
		dockerPath := opts.dockerPath
		if dockerPath == "" {
			dockerPath = "docker"
		}
		probeServer, probeErr := lifecycle.NewShellServer(dockerPath, containerID, remoteUser, logger)
		if probeErr == nil {
			probedEnv, _ := lifecycle.ProbeRemoteEnv(logger, probeServer, probeStrategy, remoteUser)
			probeServer.Close()
			for k, v := range probedEnv {
				resolvedRemoteEnv[k] = v
			}
		}
	}

	// From merged metadata
	for k, v := range merged.RemoteEnv {
		if v != nil {
			resolvedRemoteEnv[k] = *v
		}
	}

	// From config (overrides metadata)
	if loadResult != nil {
		cfg := loadResult.Config
		if cfg.RemoteUser != "" {
			remoteUser = cfg.RemoteUser
		}
		for k, v := range cfg.RemoteEnv {
			if v != nil {
				resolvedRemoteEnv[k] = *v
			}
		}
	}

	// 8. Substitute ${containerEnv:X} in resolved remoteEnv
	for k, v := range resolvedRemoteEnv {
		substituted := config.SubstituteContainer("linux", containerEnv, v)
		if s, ok := substituted.(string); ok {
			resolvedRemoteEnv[k] = s
		}
	}

	// 9. Build docker exec args
	dockerArgs := []string{"exec"}

	// Only allocate a TTY for a real interactive terminal. Forcing `-t` for
	// --log-format json with a piped stdin (exactly how the VS Code extension
	// invokes exec) makes docker fail with "cannot attach stdin to a
	// TTY-enabled container". The TS CLI only uses a PTY when isTTY is true.
	isTTY := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	if isTTY {
		dockerArgs = append(dockerArgs, "-it")
	} else {
		dockerArgs = append(dockerArgs, "-i")
	}

	if remoteUser != "" {
		dockerArgs = append(dockerArgs, "-u", remoteUser)
	}

	// Run in the container's workspace folder (remoteCwd), matching the TS CLI
	// (containerProperties.remoteWorkspaceFolder). Without this, commands run in
	// the image WORKDIR instead of the workspace.
	if loadResult != nil && loadResult.WorkspaceConfig != nil && loadResult.WorkspaceConfig.WorkspaceFolder != "" {
		dockerArgs = append(dockerArgs, "-w", loadResult.WorkspaceConfig.WorkspaceFolder)
	}

	// 10. Add resolved remoteEnv as -e flags (config+metadata, substituted)
	for k, v := range resolvedRemoteEnv {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// 11. Add CLI --remote-env flags (these override everything)
	for _, re := range opts.remoteEnvs {
		dockerArgs = append(dockerArgs, "-e", re)
	}

	dockerArgs = append(dockerArgs, containerID)
	dockerArgs = append(dockerArgs, cmdArgs...)

	dockerPath := opts.dockerPath
	if dockerPath == "" {
		dockerPath = "docker"
	}

	execCmd := exec.Command(dockerPath, dockerArgs...)
	execCmd.Stdin = os.Stdin
	if opts.logFormat == "json" {
		// In JSON mode the command's output is emitted as `raw` log events on the
		// log stream (not written to stdout), matching the TS CLI so the JSON
		// consumer (e.g. the VS Code extension) gets a clean event stream and an
		// empty stdout. Both the command's stdout and stderr become raw events.
		rw := &rawLogWriter{logger: logger}
		execCmd.Stdout = rw
		execCmd.Stderr = rw
	} else {
		// Text/interactive mode: pass through directly. docker exec -it inherits
		// the terminal when the CLI itself runs under a TTY.
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
	}

	err = execCmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &coreerrors.ExitCodeError{Code: execExitCode(exitErr), Err: err}
		}
		return &coreerrors.ExitCodeError{Code: 1, Err: err}
	}
	return nil
}

// execExitCode derives the process exit code for the 128+N contract.
//
// os/exec reports ExitCode()==-1 when the child was terminated by a signal
// rather than exiting normally. That happens here when the host `docker`
// process itself is signaled (e.g. the terminal delivers SIGINT/SIGTERM to
// the CLI's docker child). Returning -1 would surface as 255 and diverge from
// the TS CLI, which reports 128+signal (e.g. 143 for SIGTERM). On Unix we can
// recover the signal from the WaitStatus and reconstruct 128+N.
func execExitCode(exitErr *exec.ExitError) int {
	code := exitErr.ExitCode()
	if code != -1 {
		return code
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return code
}

// rawLogWriter emits everything written to it as `raw` log events at debug
// level, matching how the TS CLI streams exec output under --log-format json.
// os/exec copies a command's stdout and stderr on separate goroutines, so the
// mutex serializes writes into whole, non-interleaved raw events.
type rawLogWriter struct {
	logger log.Log
	mu     sync.Mutex
}

func (w *rawLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logger.Raw(string(p), log.LevelDebug)
	return len(p), nil
}

func findContainerByOpts(ctx context.Context, engine *docker.EngineClient, opts *execOpts, logger log.Log) string {
	labels := opts.idLabels
	if len(labels) == 0 && opts.workspaceFolder != "" {
		labels = []string{
			fmt.Sprintf("devcontainer.local_folder=%s", resolvePath(opts.workspaceFolder)),
		}
	}

	if len(labels) == 0 {
		return ""
	}

	ids, err := engine.ListContainers(ctx, false, labels)
	if err != nil || len(ids) == 0 {
		ids, err = engine.ListContainers(ctx, true, labels)
		if err != nil || len(ids) == 0 {
			return findComposeContainer(ctx, engine, opts, logger)
		}
	}

	return ids[0]
}

func findComposeContainer(ctx context.Context, engine *docker.EngineClient, opts *execOpts, logger log.Log) string {
	if opts.workspaceFolder == "" {
		return ""
	}

	ws := resolvePath(opts.workspaceFolder)
	loadResult, err := config.LoadDevContainerConfig(ws, opts.configPath, "")
	if err != nil || !loadResult.Config.IsComposeConfig() {
		return ""
	}

	cfg := loadResult.Config
	projectName := docker.ToProjectName(filepath.Base(ws)+"_devcontainer", true)

	if envName := os.Getenv("COMPOSE_PROJECT_NAME"); envName != "" {
		projectName = docker.ToProjectName(envName, true)
	}

	ids, _ := engine.ListContainers(ctx, true, []string{
		fmt.Sprintf("com.docker.compose.project=%s", projectName),
		fmt.Sprintf("com.docker.compose.service=%s", cfg.Service),
	})
	if len(ids) > 0 {
		return ids[0]
	}

	return ""
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// splitExecArgs separates devcontainer flags from the command to execute.
// Matches yargs halt-at-non-option: stops at the first non-flag argument.
func splitExecArgs(args []string) (flags []string, cmd []string) {
	valueFlags := map[string]bool{
		"--workspace-folder": true, "--config": true, "--override-config": true,
		"--docker-path": true, "--docker-compose-path": true, "--container-id": true,
		"--id-label": true, "--log-level": true, "--log-format": true, "--remote-env": true,
		"--container-data-folder": true, "--container-system-data-folder": true,
		"--default-user-env-probe": true, "--user-data-folder": true,
		"--terminal-columns": true, "--terminal-rows": true,
		"--log-file": true, "--terminal-log-file": true,
	}

	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			return flags, args[i+1:]
		}
		if !strings.HasPrefix(arg, "-") {
			return flags, args[i:]
		}
		flags = append(flags, arg)
		i++
		// If this flag takes a value, consume the next arg too
		if valueFlags[arg] && i < len(args) {
			flags = append(flags, args[i])
			i++
		}
	}
	return flags, nil
}

// Keep imports used
var _ = json.Marshal
var _ = strings.Join
