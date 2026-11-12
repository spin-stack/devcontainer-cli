package lifecycle

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

type lockedLog struct {
	log.Logger
	mu    sync.Mutex
	lines []string
}

func (l *lockedLog) Write(text string, _ ...log.Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, text)
}

func (l *lockedLog) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.lines, s)
}

func TestRunHooksSkipsOnlyPostAttachAndInstallsDotfilesAfterPostCreate(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []any{"onCreate"},
		UpdateContentCommands: []any{"updateContent"},
		PostCreateCommands:    []any{"postCreate"},
		PostStartCommands:     []any{"postStart"},
		PostAttachCommands:    []any{"postAttach"},
	}
	dotfiles := 0
	dotfilesRanBefore := func(want int) func(string) {
		return func(command string) {
			if dotfiles != want {
				t.Errorf("%s ran after %d dotfiles installs, want %d", command, dotfiles, want)
			}
		}
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	gomock.InOrder(
		exec.EXPECT().Exec("onCreate"),
		exec.EXPECT().Exec("updateContent"),
		exec.EXPECT().Exec("postCreate").Do(dotfilesRanBefore(0)),
		exec.EXPECT().Exec("postStart").Do(dotfilesRanBefore(1)),
	)
	opts := RunOptions{SkipPostAttach: true, AfterPostCreate: func() { dotfiles++ }}
	if err := RunHooks(log.Null, exec, merged, opts); err != nil {
		t.Fatal(err)
	}
	if dotfiles != 1 {
		t.Errorf("dotfiles installed %d times, want once", dotfiles)
	}
}

func TestParallelCommandHeaderOnlyForSuccess(t *testing.T) {
	l := &lockedLog{Logger: log.Null}
	cmd := map[string]any{"a": "ok-a", "b": "fails-b"}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	exec.EXPECT().Exec("ok-a")
	exec.EXPECT().Exec("fails-b").Return(exitedOne("fails-b"))
	if err := executeCommand(exec, l, PhasePostCreate, cmd, ""); err == nil {
		t.Fatal("expected b's failure")
	}
	if !l.has("Running a of postCreateCommand from devcontainer.json...") {
		t.Errorf("no header for the command that succeeded: %q", l.lines)
	}
	if l.has("Running b of postCreateCommand from devcontainer.json...") {
		t.Errorf("header printed for the command that failed: %q", l.lines)
	}
	if !l.has("b of postCreateCommand from devcontainer.json failed with exit code 1. Skipping any further user-provided commands.") {
		t.Errorf("no failure line: %q", l.lines)
	}
}

func TestFailureLineNeedsANonZeroExitCode(t *testing.T) {
	const line = "postStartCommand from devcontainer.json failed with exit code 1. Skipping any further user-provided commands."
	exitsWith := func(code int) CommandExecutor {
		exec := NewMockCommandExecutor(gomock.NewController(t))
		exec.EXPECT().Exec("x").Return(&CommandError{Command: "x", ExitCode: code})
		return exec
	}
	l := &lockedLog{Logger: log.Null}
	_ = executeCommand(exitsWith(1), l, PhasePostStart, "x", "")
	if !l.has(line) {
		t.Errorf("exit code 1: lines = %q, want %q", l.lines, line)
	}
	l = &lockedLog{Logger: log.Null}
	_ = executeCommand(exitsWith(0), l, PhasePostStart, "x", "")
	for _, got := range l.lines {
		if strings.Contains(got, "failed with exit code") {
			t.Errorf("exit code 0 reported a failure line: %q", got)
		}
	}
}

func TestCommandToStringSkipsEmptyAndNull(t *testing.T) {
	if got := commandToString(nil); got != "" {
		t.Errorf("commandToString(nil) = %q, want empty", got)
	}
	if got := commandToString(map[string]any{"a": "", "b": "echo hi", "c": nil}); got != "echo hi" {
		t.Errorf("commandToString with empty entries = %q, want %q", got, "echo hi")
	}
	if got := commandToString(map[string]any{"a": "x", "b": "y"}); got != "x && y" && got != "y && x" {
		t.Errorf("commandToString of two commands = %q, want them joined by \" && \"", got)
	}
}
