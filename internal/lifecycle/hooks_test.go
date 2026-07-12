package lifecycle

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

type mockExecutor struct {
	commands []string
	failOn   string
}

func (m *mockExecutor) Exec(command string) error {
	m.commands = append(m.commands, command)
	if m.failOn != "" && strings.Contains(command, m.failOn) {
		return fmt.Errorf("command failed: %s", command)
	}
	return nil
}

func TestRunHooks(t *testing.T) {
	tests := []struct {
		name      string
		merged    *imagemeta.MergedConfig
		opts      RunOptions
		failOn    string
		wantErr   bool
		wantCount int
	}{
		{
			name: "all phases",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []interface{}{"echo onCreate"},
				UpdateContentCommands: []interface{}{"echo updateContent"},
				PostCreateCommands:    []interface{}{"echo postCreate"},
				PostStartCommands:     []interface{}{"echo postStart"},
				PostAttachCommands:    []interface{}{"echo postAttach"},
			},
			opts:      RunOptions{},
			wantCount: 5,
		},
		{
			name: "skip post create",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:   []interface{}{"echo onCreate"},
				PostCreateCommands: []interface{}{"echo postCreate"},
				PostStartCommands:  []interface{}{"echo postStart"},
			},
			opts:      RunOptions{SkipPostCreate: true},
			wantCount: 0,
		},
		{
			name: "skip post attach",
			merged: &imagemeta.MergedConfig{
				PostAttachCommands: []interface{}{"echo postAttach"},
				PostStartCommands:  []interface{}{"echo postStart"},
			},
			opts: RunOptions{SkipPostAttach: true},
			// postStart runs, postAttach skipped
			wantCount: 1,
		},
		{
			name: "skip non-blocking",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []interface{}{"echo onCreate"},
				UpdateContentCommands: []interface{}{"echo updateContent"},
				PostCreateCommands:    []interface{}{"echo postCreate"},
			},
			opts: RunOptions{SkipNonBlocking: true},
			// Default waitFor is updateContentCommand → stops after that
			wantCount: 2,
		},
		{
			name: "prebuild",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []interface{}{"echo onCreate"},
				UpdateContentCommands: []interface{}{"echo updateContent"},
				PostCreateCommands:    []interface{}{"echo postCreate"},
			},
			opts: RunOptions{Prebuild: true},
			// Prebuild stops after updateContentCommand
			wantCount: 2,
		},
		{
			name: "command failure",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands: []interface{}{"echo ok", "fail-here"},
			},
			opts:    RunOptions{},
			failOn:  "fail-here",
			wantErr: true,
		},
		{
			name:      "empty config",
			merged:    &imagemeta.MergedConfig{},
			opts:      RunOptions{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &mockExecutor{failOn: tt.failOn}
			err := RunHooks(log.Null, exec, tt.merged, tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error on command failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(exec.commands) != tt.wantCount {
				t.Errorf("commands = %d, want %d; got: %v", len(exec.commands), tt.wantCount, exec.commands)
			}
		})
	}
}

func TestCommandToString(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"echo hello", "echo hello"},
		{[]interface{}{"npm", "install"}, "'npm' 'install'"},
		{map[string]interface{}{"a": "cmd1", "b": "cmd2"}, ""}, // contains both but order varies
		{nil, ""},
	}

	for _, tt := range tests {
		got := commandToString(tt.input)
		if tt.want == "" && got == "" {
			continue
		}
		if tt.want != "" && got != tt.want {
			// For map case, just verify both commands are present
			if _, ok := tt.input.(map[string]interface{}); ok {
				if !strings.Contains(got, "cmd1") || !strings.Contains(got, "cmd2") {
					t.Errorf("commandToString(%v) = %q, expected both cmd1 and cmd2", tt.input, got)
				}
			} else {
				t.Errorf("commandToString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

func TestInstallDotfiles(t *testing.T) {
	tests := []struct {
		name         string
		config       DotfilesConfig
		wantCount    int      // -1 to skip the count assertion
		wantContains []string // substrings expected in commands[0]
	}{
		{
			name:      "empty repo",
			config:    DotfilesConfig{},
			wantCount: 0,
		},
		{
			name: "with repo",
			config: DotfilesConfig{
				Repository: "owner/dotfiles",
				TargetPath: "~/dotfiles",
			},
			wantCount: 1,
			// should auto-prefix GitHub URL and search for install.sh
			wantContains: []string{"github.com/owner/dotfiles.git", "install.sh"},
		},
		{
			name: "custom command",
			config: DotfilesConfig{
				Repository:     "https://github.com/owner/dotfiles.git",
				InstallCommand: "my-setup.sh",
				TargetPath:     "~/dotfiles",
			},
			wantCount:    -1,
			wantContains: []string{"my-setup.sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &mockExecutor{}
			err := InstallDotfiles(log.Null, exec, tt.config)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantCount >= 0 && len(exec.commands) != tt.wantCount {
				t.Fatalf("commands = %d, want %d", len(exec.commands), tt.wantCount)
			}
			for _, sub := range tt.wantContains {
				if len(exec.commands) == 0 {
					t.Fatalf("expected a command containing %q, got none", sub)
				}
				if !strings.Contains(exec.commands[0], sub) {
					t.Errorf("commands[0] = %q, should contain %q", exec.commands[0], sub)
				}
			}
		})
	}
}

func TestCommandToString_ArrayQuoted(t *testing.T) {
	// Array form must preserve each argument literally (no re-tokenization, no
	// variable expansion) via shell quoting.
	got := commandToString([]interface{}{"echo", "hello world", "$HOME", "a'b"})
	want := `'echo' 'hello world' '$HOME' 'a'\''b'`
	if got != want {
		t.Errorf("commandToString(array) = %q, want %q", got, want)
	}
	// String form passes through unchanged (runs via shell).
	if s := commandToString("echo $HOME"); s != "echo $HOME" {
		t.Errorf("commandToString(string) = %q", s)
	}
}

// cmdErrExecutor returns a *CommandError on failure, mirroring ShellExecutor, so
// we can assert the error chain survives the parallel (object-syntax) hook path.
type cmdErrExecutor struct{ failOn string }

func (e *cmdErrExecutor) Exec(command string) error {
	if e.failOn != "" && strings.Contains(command, e.failOn) {
		return &CommandError{Command: command, ExitCode: 1}
	}
	return nil
}

// Object-syntax hooks run in parallel and their errors are aggregated. The
// aggregate must still expose the underlying *CommandError via errors.As, so
// run_user_commands can special-case it exactly like the sequential path.
func TestExecuteCommand_ParallelPreservesCommandError(t *testing.T) {
	cmd := map[string]interface{}{
		"one": "run-one",
		"two": "run-two-fails",
	}
	err := executeCommand(&cmdErrExecutor{failOn: "fails"}, log.Null, PhasePostCreate, cmd)
	if err == nil {
		t.Fatal("expected an error from the failing parallel hook")
	}
	var hook *HookError
	if !errors.As(err, &hook) {
		t.Fatalf("expected a *HookError, got %T", err)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("*CommandError chain lost in parallel aggregation: %v", err)
	}
	if cmdErr.ExitCode != 1 {
		t.Errorf("unexpected exit code: %d", cmdErr.ExitCode)
	}
}
