package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// fakeExecutor is an in-memory ContainerExecutor for testing the shell server
// without a real container.
type fakeExecutor struct {
	fn          func(command string) (stdout, stderr string, code int, err error)
	lastUser    string
	lastEnv     []string
	lastCommand string
	calls       int
}

func (f *fakeExecutor) ExecInContainer(_ context.Context, _, user string, env []string, command string) (string, string, int, error) {
	f.calls++
	f.lastUser, f.lastEnv, f.lastCommand = user, env, command
	return f.fn(command)
}

func TestShellServerExec(t *testing.T) {
	fe := &fakeExecutor{fn: func(command string) (string, string, int, error) {
		if command == "false" {
			return "", "boom\n", 42, nil
		}
		return "hello\n", "", 0, nil
	}}
	s, err := NewShellServer(t.Context(), fe, "cid", "vscode", log.Null, "FOO=bar")
	if err != nil {
		t.Fatal(err)
	}

	stdout, code, err := s.Exec("echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "hello\n" || code != 0 {
		t.Errorf("Exec = (%q, %d), want (hello, 0)", stdout, code)
	}
	// User and remoteEnv are forwarded to every exec.
	if fe.lastUser != "vscode" || len(fe.lastEnv) != 1 || fe.lastEnv[0] != "FOO=bar" {
		t.Errorf("user/env = %q/%v", fe.lastUser, fe.lastEnv)
	}

	// A non-zero exit code is surfaced (stderr goes to the log, not the return).
	stdout, code, err = s.Exec("false")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || code != 42 {
		t.Errorf("Exec(false) = (%q, %d), want (\"\", 42)", stdout, code)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
}

func TestShellServerExecError(t *testing.T) {
	fe := &fakeExecutor{fn: func(string) (string, string, int, error) {
		return "", "", -1, errors.New("attach failed")
	}}
	s, _ := NewShellServer(t.Context(), fe, "cid", "", log.Null)
	if _, _, err := s.Exec("echo hi"); err == nil {
		t.Fatal("expected the underlying exec error to propagate")
	}
}

func TestShellExecutorWorkdirAndFailure(t *testing.T) {
	fe := &fakeExecutor{fn: func(string) (string, string, int, error) { return "", "", 0, nil }}
	s, _ := NewShellServer(t.Context(), fe, "cid", "", log.Null)
	ex := &ShellExecutor{Server: s, Log: log.Null, WorkDir: "/workspaces/project"}

	if err := ex.Exec("make build"); err != nil {
		t.Fatal(err)
	}
	// WorkDir is applied by prefixing a `cd`.
	if fe.lastCommand != "cd '/workspaces/project' && make build" {
		t.Errorf("command = %q", fe.lastCommand)
	}

	// A non-zero exit becomes a CommandError carrying the original command.
	fe.fn = func(string) (string, string, int, error) { return "", "", 2, nil }
	err := ex.Exec("false")
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) || cmdErr.ExitCode != 2 || cmdErr.Command != "false" {
		t.Fatalf("err = %v, want CommandError{false, 2}", err)
	}
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "''"},
		{"plain", "/workspaces/project", "'/workspaces/project'"},
		{"embedded single quote", "/tmp/it's-here", "'/tmp/it'\"'\"'s-here'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellSingleQuote(tt.input); got != tt.want {
				t.Fatalf("shellSingleQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
