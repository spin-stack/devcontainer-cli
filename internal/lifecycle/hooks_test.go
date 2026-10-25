package lifecycle

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

//go:generate go tool mockgen -destination=mock_command_executor_test.go -package=lifecycle github.com/devcontainers/cli/internal/lifecycle CommandExecutor

// expectInOrder makes exec expect exactly commands, one after another; the one
// equal to failOn fails with "command failed: <command>", the others succeed.
func expectInOrder(exec *MockCommandExecutor, failOn string, commands ...string) {
	calls := make([]any, len(commands))
	for i, c := range commands {
		var err error
		if c == failOn {
			err = errors.New("command failed: " + c)
		}
		calls[i] = exec.EXPECT().Exec(c).Return(err)
	}
	gomock.InOrder(calls...)
}

// exitedOne is the *CommandError ShellExecutor returns for a command that
// exits 1.
func exitedOne(command string) error { return &CommandError{Command: command, ExitCode: 1} }

func TestRunHooks(t *testing.T) {
	tests := []struct {
		name    string
		merged  *imagemeta.MergedConfig
		opts    RunOptions
		failOn  string
		wantErr bool
		want    []string
	}{
		{
			name: "all phases",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []any{"echo onCreate"},
				UpdateContentCommands: []any{"echo updateContent"},
				PostCreateCommands:    []any{"echo postCreate"},
				PostStartCommands:     []any{"echo postStart"},
				PostAttachCommands:    []any{"echo postAttach"},
			},
			opts: RunOptions{},
			want: []string{"echo onCreate", "echo updateContent", "echo postCreate", "echo postStart", "echo postAttach"},
		},
		{
			name: "skip post create",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:   []any{"echo onCreate"},
				PostCreateCommands: []any{"echo postCreate"},
				PostStartCommands:  []any{"echo postStart"},
			},
			opts: RunOptions{SkipPostCreate: true},
		},
		{
			name: "skip post attach",
			merged: &imagemeta.MergedConfig{
				PostAttachCommands: []any{"echo postAttach"},
				PostStartCommands:  []any{"echo postStart"},
			},
			opts: RunOptions{SkipPostAttach: true},
			// postStart runs, postAttach skipped
			want: []string{"echo postStart"},
		},
		{
			name: "skip non-blocking",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []any{"echo onCreate"},
				UpdateContentCommands: []any{"echo updateContent"},
				PostCreateCommands:    []any{"echo postCreate"},
			},
			opts: RunOptions{SkipNonBlocking: true},
			// Default waitFor is updateContentCommand → stops after that
			want: []string{"echo onCreate", "echo updateContent"},
		},
		{
			name: "prebuild",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands:      []any{"echo onCreate"},
				UpdateContentCommands: []any{"echo updateContent"},
				PostCreateCommands:    []any{"echo postCreate"},
			},
			opts: RunOptions{Prebuild: true},
			// Prebuild stops after updateContentCommand
			want: []string{"echo onCreate", "echo updateContent"},
		},
		{
			name: "command failure",
			merged: &imagemeta.MergedConfig{
				OnCreateCommands: []any{"echo ok", "fail-here"},
			},
			opts:    RunOptions{},
			failOn:  "fail-here",
			wantErr: true,
			want:    []string{"echo ok", "fail-here"},
		},
		{
			name:   "empty config",
			merged: &imagemeta.MergedConfig{},
			opts:   RunOptions{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := NewMockCommandExecutor(gomock.NewController(t))
			expectInOrder(exec, tt.failOn, tt.want...)
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
		})
	}
}

func TestCommandToString(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{"echo hello", "echo hello"},
		{[]any{"npm", "install"}, "'npm' 'install'"},
		{map[string]any{"a": "cmd1", "b": "cmd2"}, ""}, // contains both but order varies
		{nil, ""},
	}

	for _, tt := range tests {
		got := commandToString(tt.input)
		if tt.want == "" && got == "" {
			continue
		}
		if tt.want != "" && got != tt.want {
			// For map case, just verify both commands are present
			if _, ok := tt.input.(map[string]any); ok {
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
		name            string
		config          DotfilesConfig
		wantContains    []string // substrings expected in the one command; none runs when empty
		wantNotContains []string // substrings that must NOT appear in it
	}{
		{
			name:   "empty repo",
			config: DotfilesConfig{},
		},
		{
			name: "with repo",
			config: DotfilesConfig{
				Repository: "owner/dotfiles",
				TargetPath: "~/dotfiles",
			},
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
			wantContains: []string{"my-setup.sh"},
		},
		{
			// upstream #683: a leading ~/ in the install command must expand.
			name: "install command with leading ~/ expands to $HOME",
			config: DotfilesConfig{
				Repository:     "owner/dotfiles",
				InstallCommand: "~/install.sh",
				TargetPath:     "~/dotfiles",
			},
			wantContains:    []string{`"$HOME/install.sh"`},
			wantNotContains: []string{`"~/install.sh"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := NewMockCommandExecutor(gomock.NewController(t))
			if len(tt.wantContains) > 0 {
				exec.EXPECT().Exec(gomock.Cond(func(command string) bool {
					return !slices.ContainsFunc(tt.wantContains, func(sub string) bool { return !strings.Contains(command, sub) }) &&
						!slices.ContainsFunc(tt.wantNotContains, func(sub string) bool { return strings.Contains(command, sub) })
				}))
			}
			if err := InstallDotfiles(log.Null, exec, tt.config); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommandToString_ArrayQuoted(t *testing.T) {
	// Array form must preserve each argument literally (no re-tokenization, no
	// variable expansion) via shell quoting.
	got := commandToString([]any{"echo", "hello world", "$HOME", "a'b"})
	want := `'echo' 'hello world' '$HOME' 'a'\''b'`
	if got != want {
		t.Errorf("commandToString(array) = %q, want %q", got, want)
	}
	// String form passes through unchanged (runs via shell).
	if s := commandToString("echo $HOME"); s != "echo $HOME" {
		t.Errorf("commandToString(string) = %q", s)
	}
}

// Object-syntax hooks run in parallel and their errors are aggregated. The
// aggregate must still expose the underlying *CommandError via errors.As, so
// run_user_commands can special-case it exactly like the sequential path.
func TestExecuteCommand_ParallelPreservesCommandError(t *testing.T) {
	cmd := map[string]any{
		"one": "run-one",
		"two": "run-two-fails",
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	exec.EXPECT().Exec("run-one")
	exec.EXPECT().Exec("run-two-fails").Return(exitedOne("run-two-fails"))
	err := executeCommand(exec, log.Null, PhasePostCreate, cmd, "")
	if err == nil {
		t.Fatal("expected an error from the failing parallel hook")
	}
	if _, ok := errors.AsType[*HookError](err); !ok {
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

// A failed command's error says which command it was and where it comes from,
// as TS runLifecycleCommand's ContainerError: the hook alone for a single
// command, "<name> of <hook>" for one of an object's parallel commands, and
// "Feature '<id>'" for a Feature's command.
func TestHookErrorDescribesTheCommandAndItsOrigin(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		PostCreateCommands: []any{"ok", map[string]any{"a": "ok", "b": "fails-b", "c": "fails-c"}},
		CommandOrigins:     map[string][]string{"postCreateCommand": {"./myfeat", "devcontainer.json"}},
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	exec.EXPECT().Exec("ok").Times(2)
	exec.EXPECT().Exec("fails-b").Return(exitedOne("fails-b"))
	exec.EXPECT().Exec("fails-c").Return(exitedOne("fails-c"))
	err := RunHooks(log.Null, exec, merged, RunOptions{})
	he, ok := errors.AsType[*HookError](err)
	if !ok || he.Description() != "b of postCreateCommand from devcontainer.json failed." {
		t.Fatalf("parallel: err = %v, description = %q", err, he.Description())
	}

	merged = &imagemeta.MergedConfig{
		OnCreateCommands: []any{"fails-feature"},
		CommandOrigins:   map[string][]string{"onCreateCommand": {"ghcr.io/o/f/x:1"}},
	}
	exec = NewMockCommandExecutor(gomock.NewController(t))
	exec.EXPECT().Exec("fails-feature").Return(exitedOne("fails-feature"))
	err = RunHooks(log.Null, exec, merged, RunOptions{})
	if he, ok := errors.AsType[*HookError](err); !ok || he.Description() != "onCreateCommand from Feature 'ghcr.io/o/f/x:1' failed." {
		t.Fatalf("feature: err = %v", err)
	}

	// A command appended without an origin (the config's own) is devcontainer.json's.
	merged = &imagemeta.MergedConfig{PostStartCommands: []any{"fails"}}
	exec = NewMockCommandExecutor(gomock.NewController(t))
	exec.EXPECT().Exec("fails").Return(exitedOne("fails"))
	err = RunHooks(log.Null, exec, merged, RunOptions{})
	if he, ok := errors.AsType[*HookError](err); !ok || he.Description() != "postStartCommand from devcontainer.json failed." {
		t.Fatalf("config: err = %v", err)
	}
}

// MergeConfiguration records each command's origin: a Feature entry's id, or
// devcontainer.json for the entry without one (TS lifecycleCommandOriginMapFromMetadata).
func TestMergeRecordsCommandOrigins(t *testing.T) {
	m := imagemeta.MergeConfiguration([]imagemeta.Entry{
		{ID: "./myfeat", PostCreateCommand: "a"},
		{ID: "ghcr.io/o/f/x:1", PostCreateCommand: ""},
		{PostCreateCommand: "c", OnCreateCommand: "d"},
	})
	if got := m.CommandOrigins["postCreateCommand"]; !slices.Equal(got, []string{"./myfeat", "devcontainer.json"}) {
		t.Errorf("postCreate origins = %v", got)
	}
	if got := m.CommandOrigins["onCreateCommand"]; !slices.Equal(got, []string{"devcontainer.json"}) {
		t.Errorf("onCreate origins = %v", got)
	}
}

// TS runs an array command without a shell, and its failure names the
// arguments joined with spaces; a string command's names /bin/sh -c. The same
// holds for an array among an object's parallel commands.
func TestArrayCommandFailureNamesItsArguments(t *testing.T) {
	failing := func() CommandExecutor {
		exec := NewMockCommandExecutor(gomock.NewController(t))
		exec.EXPECT().Exec(gomock.Cond(func(command string) bool { return strings.Contains(command, "fails") })).DoAndReturn(exitedOne)
		return exec
	}
	array := []any{"/bin/bash", "-i", "-c", "python3 -m venv $HOME/.local && fails"}
	for name, cmd := range map[string]any{
		"single":   array,
		"parallel": map[string]any{"setup": array},
	} {
		err := executeCommand(failing(), log.Null, PhasePostStart, cmd, "")
		ce, ok := errors.AsType[*CommandError](err)
		if !ok {
			t.Fatalf("%s: err = %v, want a *CommandError", name, err)
		}
		if want := "Command failed: /bin/bash -i -c python3 -m venv $HOME/.local && fails"; ce.Error() != want {
			t.Errorf("%s: message = %q, want %q", name, ce.Error(), want)
		}
	}
	err := executeCommand(failing(), log.Null, PhasePostStart, "echo fails", "")
	if ce, ok := errors.AsType[*CommandError](err); !ok || ce.Error() != "Command failed: /bin/sh -c echo fails" {
		t.Errorf("string command: err = %v", err)
	}
}
