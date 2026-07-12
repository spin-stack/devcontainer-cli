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
	log         log.Log
}

// NewShellServer binds a ShellServer to a container. user (docker exec -u) and
// remoteEnv entries ("KEY=VALUE", docker exec -e) are applied to every command,
// matching the TS CLI which runs lifecycle hooks and the userEnvProbe as the
// remote user.
func NewShellServer(ctx context.Context, exec ContainerExecutor, containerID, user string, logger log.Log, remoteEnv ...string) (*ShellServer, error) {
	return &ShellServer{
		ctx:         ctx,
		exec:        exec,
		containerID: containerID,
		user:        user,
		env:         remoteEnv,
		log:         logger,
	}, nil
}

// Exec runs a command inside the container and returns its stdout and exit code.
// The command's stderr is surfaced on the log (the TS CLI streams hook stderr)
// rather than returned, so a failing hook still produces diagnostics.
func (s *ShellServer) Exec(command string) (stdout string, exitCode int, err error) {
	out, errText, code, err := s.exec.ExecInContainer(s.ctx, s.containerID, s.user, s.env, command)
	if err != nil {
		return "", -1, err
	}
	if strings.TrimSpace(errText) != "" && s.log != nil {
		s.log.Write(errText, log.LevelInfo)
	}
	return out, code, nil
}

// Close is a no-op: each command is its own exec, so there is no persistent
// process to tear down. Retained for API symmetry with callers that defer it.
func (s *ShellServer) Close() error { return nil }

// ShellExecutor wraps ShellServer to implement the CommandExecutor interface.
type ShellExecutor struct {
	Server  *ShellServer
	Log     log.Log
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
	stdout, code, err := e.Server.Exec(command)
	if err != nil {
		return err
	}
	if stdout != "" {
		e.Log.Write(stdout, log.LevelInfo)
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
