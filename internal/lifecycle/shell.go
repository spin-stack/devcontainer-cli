package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

// ContainerExecutor runs a single command inside a container (one exec per call)
// and returns its stdout, stderr and exit code. *docker.EngineClient implements
// it via the Docker exec API, so the lifecycle shell needs no `docker exec`
// subprocess.
type ContainerExecutor interface {
	ExecInContainer(ctx context.Context, containerID, user string, env []string, command string) (stdout, stderr string, exitCode int, err error)
}

// ShellServer runs lifecycle commands inside a container as the remote user via
// the Docker exec API. Each command is an independent `/bin/sh -c` exec — the
// devcontainer lifecycle hooks and the userEnvProbe do not rely on shell state
// carried across commands (each probe command spawns its own login/interactive
// shell), so there is no persistent shell to manage.
type ShellServer struct {
	ctx         context.Context
	exec        ContainerExecutor
	containerID string
	user        string
	env         []string // remoteEnv "KEY=VALUE", applied to every exec
}

// NewShellServer binds a ShellServer to a container. user (docker exec -u) and
// remoteEnv entries ("KEY=VALUE", docker exec -e) are applied to every command,
// matching the TS CLI which runs lifecycle hooks and the userEnvProbe as the
// remote user.
// The logger param is accepted for call-site stability and future use; the
// server currently logs nothing itself (callers surface command output).
func NewShellServer(ctx context.Context, exec ContainerExecutor, containerID, user string, _ log.Logger, remoteEnv ...string) (*ShellServer, error) {
	return &ShellServer{
		ctx:         ctx,
		exec:        exec,
		containerID: containerID,
		user:        user,
		env:         remoteEnv,
	}, nil
}

// Exec runs a command inside the container and returns its stdout and exit code.
// stderr is intentionally NOT surfaced: the only direct caller is the internal
// userEnvProbe, whose interactive-login shell (`bash -lic`) prints a benign
// "cannot set terminal process group / no job control" warning to stderr on a
// container without a controlling TTY — the TS CLI does not show it either.
// Lifecycle hooks surface their own stderr via ShellExecutor (ExecWithStderr).
func (s *ShellServer) Exec(command string) (stdout string, exitCode int, err error) {
	out, _, code, err := s.exec.ExecInContainer(s.ctx, s.containerID, s.user, s.env, command)
	if err != nil {
		return "", -1, err
	}
	return out, code, nil
}

// ExecWithStderr is Exec but also returns the command's stderr, for callers that
// surface command diagnostics (lifecycle hooks).
func (s *ShellServer) ExecWithStderr(command string) (stdout, stderr string, exitCode int, err error) {
	out, errText, code, err := s.exec.ExecInContainer(s.ctx, s.containerID, s.user, s.env, command)
	if err != nil {
		return "", "", -1, err
	}
	return out, errText, code, nil
}

// Close is a no-op: each command is its own exec, so there is no persistent
// process to tear down. Retained for API symmetry with callers that defer it.
func (s *ShellServer) Close() error { return nil }

// ShellExecutor wraps ShellServer to implement the CommandExecutor interface.
type ShellExecutor struct {
	Server  *ShellServer
	Log     log.Logger
	WorkDir string
}

// CommandError records a failed lifecycle shell command.
type CommandError struct {
	Command  string
	ExitCode int
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("Command failed: /bin/sh -c %s", e.Command)
}

// Exec implements CommandExecutor.
func (e *ShellExecutor) Exec(command string) error {
	originalCommand := command
	if e.WorkDir != "" {
		command = fmt.Sprintf("cd %s && %s", shellSingleQuote(e.WorkDir), command)
	}
	stdout, stderr, code, err := e.Server.ExecWithStderr(command)
	if err != nil {
		return err
	}
	if stdout != "" {
		e.Log.Write(stdout, log.LevelInfo)
	}
	// Surface hook stderr (the TS CLI streams lifecycle-hook stderr) so a failing
	// or chatty hook still produces diagnostics.
	if strings.TrimSpace(stderr) != "" {
		e.Log.Write(stderr, log.LevelInfo)
	}
	if code != 0 {
		return &CommandError{Command: originalCommand, ExitCode: code}
	}
	return nil
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
