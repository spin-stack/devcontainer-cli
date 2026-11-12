package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

type ContainerExecutor interface {
	ExecInContainer(ctx context.Context, containerID, user string, env []string, command string) (stdout, stderr string, exitCode int, err error)
}

type ShellServer struct {
	ctx         context.Context
	exec        ContainerExecutor
	containerID string
	user        string
	env         []string
}

func NewShellServer(ctx context.Context, exec ContainerExecutor, containerID, user string, remoteEnv ...string) *ShellServer {
	return &ShellServer{
		ctx:         ctx,
		exec:        exec,
		containerID: containerID,
		user:        user,
		env:         remoteEnv,
	}
}

// Exec drops stderr: the userEnvProbe's `bash -lic` prints a benign "cannot set
// terminal process group / no job control" warning on a container without a
// controlling TTY.
func (s *ShellServer) Exec(command string) (stdout string, exitCode int, err error) {
	out, _, code, err := s.exec.ExecInContainer(s.ctx, s.containerID, s.user, s.env, command)
	if err != nil {
		return "", -1, err
	}
	return out, code, nil
}

func (s *ShellServer) ExecWithStderr(command string) (stdout, stderr string, exitCode int, err error) {
	out, errText, code, err := s.exec.ExecInContainer(s.ctx, s.containerID, s.user, s.env, command)
	if err != nil {
		return "", "", -1, err
	}
	return out, errText, code, nil
}

func (s *ShellServer) Close() error { return nil }

type ShellExecutor struct {
	Server  *ShellServer
	Log     log.Logger
	WorkDir string
}

type CommandError struct {
	Command  string
	ExitCode int
	Argv     []string
}

func (e *CommandError) Error() string {
	if e.Argv != nil {
		return "Command failed: " + strings.Join(e.Argv, " ")
	}
	return fmt.Sprintf("Command failed: /bin/sh -c %s", e.Command)
}

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
