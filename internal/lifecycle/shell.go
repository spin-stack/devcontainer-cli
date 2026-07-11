package lifecycle

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/devcontainers/cli/internal/core/log"
)

// EOT is the End-Of-Transmission marker used to delimit command output.
// Matches the TS '\u2404' character.
const EOT = "\u2404"

// ShellServer launches a persistent shell inside a container via `docker exec`
// and serializes command execution using EOT markers.
type ShellServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bufio.Reader
	mu     sync.Mutex
	log    log.Log
}

// NewShellServer starts a shell inside the container.
// When user is non-empty the shell runs as that user (docker exec -u), matching
// the TS CLI which runs lifecycle hooks and the userEnvProbe as the remoteUser.
// remoteEnv entries ("KEY=VALUE") are injected via docker exec -e flags.
func NewShellServer(dockerPath, containerID, user string, logger log.Log, remoteEnv ...string) (*ShellServer, error) {
	if dockerPath == "" {
		dockerPath = "docker"
	}

	args := []string{"exec", "-i"}
	if user != "" {
		args = append(args, "-u", user)
	}
	for _, e := range remoteEnv {
		args = append(args, "-e", e)
	}
	args = append(args, containerID, "/bin/sh")
	cmd := exec.Command(dockerPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start shell: %w", err)
	}

	return &ShellServer{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		stderr: bufio.NewReader(stderrPipe),
		log:    logger,
	}, nil
}

// Exec runs a command inside the shell server and returns stdout and exit code.
// Commands are serialized — only one runs at a time.
func (s *ShellServer) Exec(command string) (stdout string, exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Protocol: echo EOT marker, run command, echo EOT+exitcode+EOT, echo EOT to stderr
	wrapped := fmt.Sprintf("echo -n %s; ( %s ); echo -n %s$?%s; echo -n %s >&2\n", EOT, command, EOT, EOT, EOT)

	if _, err := io.WriteString(s.stdin, wrapped); err != nil {
		return "", -1, fmt.Errorf("write command: %w", err)
	}

	// Read stdout until we get the exit code between EOT markers
	stdoutResult, codeStr, err := readUntilEOT(s.stdout)
	if err != nil {
		return "", -1, fmt.Errorf("read stdout: %w", err)
	}

	// Read the command's stderr up to the EOT marker and surface it on the log
	// instead of discarding it — otherwise a failing lifecycle hook gives no
	// diagnostics (the TS CLI streams hook stderr).
	if stderrText, _ := readSegment(s.stderr); strings.TrimSpace(stderrText) != "" && s.log != nil {
		s.log.Write(stderrText, log.LevelInfo)
	}

	code := 0
	if codeStr != "" {
		fmt.Sscanf(codeStr, "%d", &code)
	}

	return stdoutResult, code, nil
}

// Close terminates the shell server.
func (s *ShellServer) Close() error {
	if s.stdin != nil {
		_, _ = io.WriteString(s.stdin, "exit\n")
		_ = s.stdin.Close()
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- s.cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return err
	case <-time.After(2 * time.Second):
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		return <-waitCh
	}
}

// readUntilEOT reads from the reader collecting text between EOT markers.
// Returns (stdout_text, exit_code_string, error).
// Protocol: {EOT}{stdout}{EOT}{exitcode}{EOT}
func readUntilEOT(r *bufio.Reader) (string, string, error) {
	// Wait for first EOT (start marker)
	if err := skipUntilEOT(r); err != nil {
		return "", "", err
	}

	// Read stdout until second EOT
	stdout, err := readSegment(r)
	if err != nil {
		return "", "", err
	}

	// Read exit code until third EOT
	codeStr, err := readSegment(r)
	if err != nil {
		return stdout, "", err
	}

	return stdout, codeStr, nil
}

func skipUntilEOT(r *bufio.Reader) error {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		// EOT is multi-byte UTF-8: \xe2\x90\x84
		if b == 0xe2 {
			b2, _ := r.ReadByte()
			b3, _ := r.ReadByte()
			if b2 == 0x90 && b3 == 0x84 {
				return nil
			}
		}
	}
}

func readSegment(r *bufio.Reader) (string, error) {
	var buf strings.Builder
	for {
		b, err := r.ReadByte()
		if err != nil {
			return buf.String(), err
		}
		if b == 0xe2 {
			b2, _ := r.ReadByte()
			b3, _ := r.ReadByte()
			if b2 == 0x90 && b3 == 0x84 {
				return buf.String(), nil
			}
			buf.WriteByte(b)
			buf.WriteByte(b2)
			buf.WriteByte(b3)
		} else {
			buf.WriteByte(b)
		}
	}
}

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
