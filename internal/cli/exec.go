package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
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
	"github.com/moby/moby/api/types/container"
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
	logFile             string
	terminalLogFile     string
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
			// DisableFlagParsing skips the root's PersistentPreRunE validation, so
			// the global OCI auth flags are validated here, once parsed.
			if err := applyOCIAuthPolicy(cmd); err != nil {
				return err
			}

			if len(cmdArgs) == 0 {
				return fmt.Errorf("exec requires at least 1 arg")
			}
			return runExec(cmd.Context(), &opts, cmdArgs)
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
	f.StringArrayVar(&opts.remoteEnvs, "remote-env", nil, "Remote env vars.")
	f.String("docker-compose-path", "", "")
	f.String("container-data-folder", "", "")
	f.String("container-system-data-folder", "", "")
	f.Bool("mount-workspace-git-root", true, "")
	f.Bool("mount-git-worktree-common-dir", false, "")
	f.StringVar(&opts.defaultUserEnvProbe, "default-user-env-probe", "loginInteractiveShell", "")
	f.Bool("skip-feature-auto-mapping", false, "")
	_ = f.MarkHidden("skip-feature-auto-mapping") // hidden testing flag (TS parity)
	f.String("user-data-folder", "", "")
	f.IntVar(&opts.terminalColumns, "terminal-columns", 0, "")
	f.IntVar(&opts.terminalRows, "terminal-rows", 0, "")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

// runExec runs a command in a running dev container through `docker exec`.
// What it decides is in the helpers below; this is their wiring to the
// configuration, the engine and the docker CLI, which the parity lane runs with
// the built binary against real containers:
//
// mutate-lane: TestParityMatrix (exec.workspace-image-success,
// exec.workspace-compose-success, exec.container-config-success,
// exec.container-metadata-success, exec.workspace-image-json-raw-output-success,
// exec.workspace-image-nonzero-exit, exec.container-not-found,
// exec.config-remote-user-probe-success).
func runExec(ctx context.Context, opts *execOpts, cmdArgs []string) error {
	if err := validateIDLabels(opts.idLabels); err != nil {
		return err
	}
	if err := validateRemoteEnvs(opts.remoteEnvs); err != nil {
		return err
	}
	// 0.88: default --workspace-folder to cwd when no --container-id/--id-label/--workspace-folder.
	if execDefaultsToCwd(opts) {
		opts.workspaceFolder, _ = os.Getwd()
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

	logger := log.New(log.Options{
		Version: cliVersion(),
		Level:   log.ParseLevel(opts.logLevel),
		Format:  opts.logFormat,
		Writer:  logDst,
	})

	// 1. If workspace-folder provided without container-id, validate config exists early
	var loadResult *config.LoadResult
	if opts.workspaceFolder != "" {
		cwd, _ := os.Getwd()
		var loadErr error
		loadResult, loadErr = config.LoadDevContainerConfig(resolvePath(opts.workspaceFolder), execConfigPath(opts.configPath, cwd), "")
		if err := execConfigError(loadErr, opts.containerID); err != nil {
			return err
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
		configFile := ""
		if loadResult != nil {
			configFile = loadResult.Config.ConfigFilePath
		}
		containerID = findContainerByOpts(ctx, engine, opts, configFile, logger)
	}

	if containerID == "" {
		return fmt.Errorf("Dev container not found.")
	}

	// 3. Inspect container
	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("Dev container not found.")
	}
	if !containerRunning(inspect.State) {
		return fmt.Errorf("Dev container is not running.")
	}
	containerCfg := cmp.Or(inspect.Config, &container.Config{})

	// 4-6. The container's env, and its image metadata from the labels.
	containerEnv := config.EnvMap(containerCfg.Env)
	var cfg *config.DevContainer
	hostSub, configFile := config.EnvHostSubContext(), ""
	if loadResult != nil {
		cfg, hostSub, configFile = loadResult.Config, loadResult.HostSub, loadResult.Config.ConfigFilePath
	}
	// TS getImageMetadataFromContainer, with the host substitutions alone.
	idLabels := containerIDLabels(opts.idLabels, containerCfg.Labels, resolvePath(opts.workspaceFolder), configFile)
	entries, err := imageMetadataFromContainer(containerCfg.Labels, cfg, nil, idLabels, hostMetadataSubstitution(hostSub, nil), logger)
	if err != nil {
		return err
	}
	merged := imagemeta.MergeConfiguration(entries)
	// The user is the merged configuration's remoteUser - the configuration's,
	// else the image metadata's - else the container's, else root; the
	// environment is probed as that user (TS createContainerProperties with
	// mergedConfig.remoteUser).
	remoteUser := execRemoteUser(cmp.Or(merged.RemoteUser, containerCfg.User, "root"), cfg)

	// 7-8. Probe the user environment, then merge and substitute it.
	probed := lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, remoteUser, execProbeStrategy(opts.defaultUserEnvProbe, cfg), "")
	resolvedRemoteEnv, err := execRemoteEnv(probed, merged.RemoteEnv, cfg, containerEnv)
	if err != nil {
		return err
	}

	// 9-11. Build the docker exec args.
	remoteWorkspace := ""
	if loadResult != nil && loadResult.WorkspaceConfig != nil {
		remoteWorkspace = loadResult.WorkspaceConfig.WorkspaceFolder
	}
	// Only allocate a TTY for a real interactive terminal. Forcing `-t` for
	// --log-format json with a piped stdin (exactly how the VS Code extension
	// invokes exec) makes docker fail with "cannot attach stdin to a
	// TTY-enabled container". The TS CLI only uses a PTY when isTTY is true.
	isTTY := isTerminalWriter(os.Stdin) && isTerminalWriter(os.Stdout)
	dockerArgs := execDockerArgs(isTTY, remoteUser, remoteWorkspace, resolvedRemoteEnv, opts.remoteEnvs, containerID, cmdArgs)

	execCmd := exec.Command(cmp.Or(opts.dockerPath, "docker"), dockerArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout, execCmd.Stderr = execOutput(opts.logFormat, logger)
	return execRunError(execCmd.Run())
}

// execDefaultsToCwd reports whether exec has nothing to find the container by,
// so the current directory is the workspace folder.
func execDefaultsToCwd(opts *execOpts) bool {
	return opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.containerID == ""
}

// execConfigPath resolves --config against the current directory.
func execConfigPath(configPath, cwd string) string {
	if configPath != "" && !filepath.IsAbs(configPath) {
		return filepath.Join(cwd, configPath)
	}
	return configPath
}

// execConfigError is the configuration error that stops exec: with
// --container-id the container is known, and a configuration that does not
// load only loses its settings.
func execConfigError(loadErr error, containerID string) error {
	if loadErr != nil && containerID == "" {
		return loadErr
	}
	return nil
}

// containerRunning reports whether an inspected container is running.
func containerRunning(state *container.State) bool {
	return state != nil && state.Running
}

// execRemoteUser is the user exec probes the environment as and runs as: the
// configuration's remoteUser, else metadataUser.
func execRemoteUser(metadataUser string, cfg *config.DevContainer) string {
	if cfg != nil && cfg.RemoteUser != "" {
		return cfg.RemoteUser
	}
	return metadataUser
}

// execProbeStrategy is the configuration's userEnvProbe, else the flag's.
func execProbeStrategy(flag string, cfg *config.DevContainer) lifecycle.UserEnvProbeStrategy {
	if cfg != nil && cfg.UserEnvProbe != "" {
		return lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}
	return lifecycle.UserEnvProbeStrategy(flag)
}

// execRemoteEnv merges the remote environment, each layer over the one before:
// the probed user environment, the image metadata's remoteEnv, the
// configuration's; a null value leaves the layer below. ${containerEnv:X} is
// then substituted from the container's environment.
func execRemoteEnv(probed map[string]string, metadata map[string]*string, cfg *config.DevContainer, containerEnv map[string]string) (map[string]string, error) {
	resolved := map[string]string{}
	maps.Copy(resolved, probed)
	layers := []map[string]*string{metadata}
	if cfg != nil {
		layers = append(layers, cfg.RemoteEnv)
	}
	for _, layer := range layers {
		for k, v := range layer {
			if v != nil {
				resolved[k] = *v
			}
		}
	}
	for k, v := range resolved {
		substituted, err := resolveContainerVariables(containerEnv, v)
		if err != nil {
			return nil, fmt.Errorf("resolve container variables: %w", err)
		}
		if s, ok := substituted.(string); ok {
			resolved[k] = s
		}
	}
	return resolved, nil
}

// execDockerArgs is the `docker exec` command line: interactive, with a TTY
// when exec runs in one, as the remote user, in the container's workspace
// folder (remoteCwd, as TS containerProperties.remoteWorkspaceFolder) when
// known, with the resolved remote environment and then the --remote-env flags,
// which docker applies last so they win.
func execDockerArgs(tty bool, remoteUser, remoteWorkspace string, remoteEnv map[string]string, cliEnvs []string, containerID string, cmdArgs []string) []string {
	args := []string{"exec", "-i"}
	if tty {
		args[1] = "-it"
	}
	if remoteUser != "" {
		args = append(args, "-u", remoteUser)
	}
	if remoteWorkspace != "" {
		args = append(args, "-w", remoteWorkspace)
	}
	for k, v := range remoteEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for _, re := range cliEnvs {
		args = append(args, "-e", re)
	}
	args = append(args, containerID)
	return append(args, cmdArgs...)
}

// execOutput is where the command's output goes. In JSON mode it is emitted as
// `raw` log events on the log stream (not written to stdout), matching the TS
// CLI so the JSON consumer (e.g. the VS Code extension) gets a clean event
// stream and an empty stdout; both streams become raw events. Otherwise it
// passes through: docker exec -it inherits the terminal when the CLI runs in one.
func execOutput(logFormat string, logger log.Logger) (stdout, stderr io.Writer) {
	if logFormat == "json" {
		rw := &rawLogWriter{logger: logger}
		return rw, rw
	}
	return os.Stdout, os.Stderr
}

// execRunError is exec's result for how `docker exec` ended: its exit code (a
// signal as 128+N), or 1 when it could not run.
func execRunError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &coreerrors.ExitCodeError{Code: execExitCode(exitErr), Err: err}
	}
	return &coreerrors.ExitCodeError{Code: 1, Err: err}
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
	// mutate-exempt: ExitCode is -1 only for a child a signal ended, whose WaitStatus is always there and Signaled, so || takes the same branch
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
	logger log.Logger
	mu     sync.Mutex
}

func (w *rawLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logger.Raw(string(p), log.LevelDebug)
	return len(p), nil
}

func findContainerByOpts(ctx context.Context, engine *docker.EngineClient, opts *execOpts, configFile string, logger log.Logger) string {
	// [local_folder, config_file] disambiguates when a project has multiple
	// devcontainer.json configs; falls back to local_folder for legacy containers.
	if id := resolveContainerID(ctx, engine, opts.workspaceFolder, configFile, opts.idLabels); id != "" {
		return id
	}
	return findComposeContainer(ctx, engine, opts, logger)
}

func findComposeContainer(ctx context.Context, engine *docker.EngineClient, opts *execOpts, logger log.Logger) string {
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
		"--" + flagAllowCrossOriginAuthHos: true,
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
