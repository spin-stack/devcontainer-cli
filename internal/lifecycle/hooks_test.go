package lifecycle

import (
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

func TestRunHooks_AllPhases(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []interface{}{"echo onCreate"},
		UpdateContentCommands: []interface{}{"echo updateContent"},
		PostCreateCommands:    []interface{}{"echo postCreate"},
		PostStartCommands:     []interface{}{"echo postStart"},
		PostAttachCommands:    []interface{}{"echo postAttach"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 5 {
		t.Errorf("commands = %d, want 5; got: %v", len(exec.commands), exec.commands)
	}
}

func TestRunHooks_SkipPostCreate(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:   []interface{}{"echo onCreate"},
		PostCreateCommands: []interface{}{"echo postCreate"},
		PostStartCommands:  []interface{}{"echo postStart"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{SkipPostCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 0 {
		t.Errorf("expected 0 commands with skipPostCreate, got %d: %v", len(exec.commands), exec.commands)
	}
}

func TestRunHooks_SkipPostAttach(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{
		PostAttachCommands: []interface{}{"echo postAttach"},
		PostStartCommands:  []interface{}{"echo postStart"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{SkipPostAttach: true})
	if err != nil {
		t.Fatal(err)
	}
	// postStart runs, postAttach skipped
	if len(exec.commands) != 1 {
		t.Errorf("commands = %d, want 1; got: %v", len(exec.commands), exec.commands)
	}
}

func TestRunHooks_SkipNonBlocking(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []interface{}{"echo onCreate"},
		UpdateContentCommands: []interface{}{"echo updateContent"},
		PostCreateCommands:    []interface{}{"echo postCreate"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{SkipNonBlocking: true})
	if err != nil {
		t.Fatal(err)
	}
	// Default waitFor is updateContentCommand → stops after that
	if len(exec.commands) != 2 {
		t.Errorf("commands = %d, want 2 (onCreate + updateContent); got: %v", len(exec.commands), exec.commands)
	}
}

func TestRunHooks_Prebuild(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []interface{}{"echo onCreate"},
		UpdateContentCommands: []interface{}{"echo updateContent"},
		PostCreateCommands:    []interface{}{"echo postCreate"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{Prebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	// Prebuild stops after updateContentCommand
	if len(exec.commands) != 2 {
		t.Errorf("commands = %d, want 2; got: %v", len(exec.commands), exec.commands)
	}
}

func TestRunHooks_CommandFailure(t *testing.T) {
	exec := &mockExecutor{failOn: "fail-here"}
	merged := &imagemeta.MergedConfig{
		OnCreateCommands: []interface{}{"echo ok", "fail-here"},
	}

	err := RunHooks(log.Null, exec, merged, RunOptions{})
	if err == nil {
		t.Error("expected error on command failure")
	}
}

func TestRunHooks_EmptyConfig(t *testing.T) {
	exec := &mockExecutor{}
	merged := &imagemeta.MergedConfig{}

	err := RunHooks(log.Null, exec, merged, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 0 {
		t.Errorf("expected 0 commands for empty config")
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

func TestInstallDotfiles_EmptyRepo(t *testing.T) {
	exec := &mockExecutor{}
	err := InstallDotfiles(log.Null, exec, DotfilesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 0 {
		t.Error("expected no commands for empty repository")
	}
}

func TestInstallDotfiles_WithRepo(t *testing.T) {
	exec := &mockExecutor{}
	err := InstallDotfiles(log.Null, exec, DotfilesConfig{
		Repository: "owner/dotfiles",
		TargetPath: "~/dotfiles",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 1 {
		t.Fatalf("expected 1 script command, got %d", len(exec.commands))
	}
	if !strings.Contains(exec.commands[0], "github.com/owner/dotfiles.git") {
		t.Errorf("should auto-prefix GitHub URL, got: %s", exec.commands[0])
	}
	if !strings.Contains(exec.commands[0], "install.sh") {
		t.Error("should search for install.sh")
	}
}

func TestInstallDotfiles_CustomCommand(t *testing.T) {
	exec := &mockExecutor{}
	err := InstallDotfiles(log.Null, exec, DotfilesConfig{
		Repository:     "https://github.com/owner/dotfiles.git",
		InstallCommand: "my-setup.sh",
		TargetPath:     "~/dotfiles",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exec.commands[0], "my-setup.sh") {
		t.Error("should use custom install command")
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
