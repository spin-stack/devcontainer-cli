package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	terminalColumns     givenInt
	terminalRows        givenInt
	defaultUserEnvProbe string
}

func newExecCmd() *cobra.Command {
	var opts execOpts

	cmd := &cobra.Command{
		Use:                "exec [cmd] [args...]",
		Short:              "Execute a command on a running dev container",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, rawArgs []string) error {
			flagArgs, cmdArgs := splitExecArgs(rawArgs)

			// `exec --help`/`-h` (before the command) prints usage and exits 0,
			// matching the TS CLI. Without this, DisableFlagParsing swallows the
			// flag and the empty command triggers a spurious "requires at least 1 arg".
			for _, a := range flagArgs {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}

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
	_ = f.MarkHidden("skip-feature-auto-mapping")
	f.String("user-data-folder", "", "")
	f.Var(&opts.terminalColumns, "terminal-columns", "")
	f.Var(&opts.terminalRows, "terminal-rows", "")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

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

	cwd, _ := os.Getwd()
	loadResult, loadErr := execLoadConfig(opts, cwd)
	if loadErr != nil {
		return loadErr
	}

	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return fmt.Errorf("Docker engine: %w", err)
	}
	defer engine.Close()

	containerID := opts.containerID
	if containerID == "" {
		configFile := ""
		if loadResult != nil {
			configFile = loadResult.Config.ConfigFilePath
		}
		var lookupErr error
		if containerID, lookupErr = findContainerByOpts(ctx, engine, opts, configFile, logger); lookupErr != nil {
			return lookupErr
		}
	}

	if containerID == "" {
		return fmt.Errorf("Dev container not found.")
	}

	inspect, err := engine.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("Dev container not found.")
	}
	if !containerRunning(inspect.State) {
		return fmt.Errorf("Dev container is not running.")
	}
	containerCfg := cmp.Or(inspect.Config, &container.Config{})

	containerEnv := config.EnvMap(containerCfg.Env)
	var cfg *config.DevContainer
	hostSub, configFile := config.EnvHostSubContext(), ""
	if loadResult != nil {
		cfg, hostSub, configFile = loadResult.Config, loadResult.HostSub, loadResult.Config.ConfigFilePath
	}
	idLabels := containerIDLabels(opts.idLabels, containerCfg.Labels, resolvePath(opts.workspaceFolder), configFile)
	entries, err := imageMetadataFromContainer(containerCfg.Labels, cfg, nil, idLabels, hostMetadataSubstitution(hostSub, nil), logger)
	if err != nil {
		return err
	}
	merged := imagemeta.MergeConfiguration(entries)
	var rawConfig any
	if loadResult != nil {
		rawConfig = loadResult.Raw
	}
	if err := checkContainerSubstitution(containerEnv, configFile, rawConfig, merged); err != nil {
		return err
	}
	remoteUser := execRemoteUser(cmp.Or(merged.RemoteUser, containerCfg.User, "root"), cfg)

	probed := lifecycle.ProbedRemoteEnv(ctx, engine, logger, containerID, remoteUser, execProbeStrategy(opts.defaultUserEnvProbe, cfg), "")
	resolvedRemoteEnv, err := execRemoteEnv(probed, merged.RemoteEnv, cfg, containerEnv)
	if err != nil {
		return err
	}

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

func execDefaultsToCwd(opts *execOpts) bool {
	return opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.containerID == ""
}

func execConfigPath(configPath, cwd string) string {
	if configPath != "" && !filepath.IsAbs(configPath) {
		return filepath.Join(cwd, configPath)
	}
	return configPath
}

func execLoadConfig(opts *execOpts, cwd string) (*config.LoadResult, error) {
	if opts.workspaceFolder == "" && opts.configPath == "" && opts.overrideConfig == "" {
		return nil, nil
	}
	ws := ""
	if opts.workspaceFolder != "" {
		ws = resolvePath(opts.workspaceFolder)
	}
	configPath := execConfigPath(opts.configPath, cwd)
	loaded, err := config.LoadDevContainerConfig(ws, configPath, execConfigPath(opts.overrideConfig, cwd))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &coreerrors.ContainerError{Description: fmt.Sprintf("Dev container config (%s) not found.", configPath)}
	}
	return loaded, err
}

func containerRunning(state *container.State) bool {
	return state != nil && state.Running
}

func execRemoteUser(metadataUser string, cfg *config.DevContainer) string {
	if cfg != nil && cfg.RemoteUser != "" {
		return cfg.RemoteUser
	}
	return metadataUser
}

func execProbeStrategy(flag string, cfg *config.DevContainer) lifecycle.UserEnvProbeStrategy {
	if cfg != nil && cfg.UserEnvProbe != "" {
		return lifecycle.UserEnvProbeStrategy(cfg.UserEnvProbe)
	}
	return lifecycle.UserEnvProbeStrategy(flag)
}

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
	configFile := ""
	if cfg != nil {
		configFile = cfg.ConfigFilePath
	}
	for k, v := range resolved {
		substituted, err := resolveContainerVariables(containerEnv, configFile, v)
		if err != nil {
			return nil, err
		}
		if s, ok := substituted.(string); ok {
			resolved[k] = s
		}
	}
	return resolved, nil
}

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

func execOutput(logFormat string, logger log.Logger) (stdout, stderr io.Writer) {
	if logFormat == "json" {
		rw := &rawLogWriter{logger: logger}
		return rw, rw
	}
	return os.Stdout, os.Stderr
}

func execRunError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &coreerrors.ExitCodeError{Code: execExitCode(exitErr), Err: err}
	}
	return &coreerrors.ExitCodeError{Code: 1, Err: err}
}

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

func findContainerByOpts(ctx context.Context, engine *docker.EngineClient, opts *execOpts, configFile string, logger log.Logger) (string, error) {
	id, err := resolveContainerID(ctx, engine, opts.workspaceFolder, configFile, opts.idLabels)
	if err != nil || id != "" {
		return id, err
	}
	return findComposeContainer(ctx, engine, opts, logger), nil
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
		if valueFlags[arg] && i < len(args) {
			flags = append(flags, args[i])
			i++
		}
	}
	return flags, nil
}

var _ = json.Marshal
var _ = strings.Join
