package lifecycle

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func TestSpecLifecycleOrder(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []any{"cmd-onCreate"},
		UpdateContentCommands: []any{"cmd-updateContent"},
		PostCreateCommands:    []any{"cmd-postCreate"},
		PostStartCommands:     []any{"cmd-postStart"},
		PostAttachCommands:    []any{"cmd-postAttach"},
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	expectInOrder(exec, "", "cmd-onCreate", "cmd-updateContent", "cmd-postCreate", "cmd-postStart", "cmd-postAttach")
	if err := RunHooks(log.Null, exec, merged, RunOptions{}); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}
}

func TestSpecWaitForGatesExecution(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		WaitFor:               "updateContentCommand",
		OnCreateCommands:      []any{"cmd-onCreate"},
		UpdateContentCommands: []any{"cmd-updateContent"},
		PostCreateCommands:    []any{"cmd-postCreate"},
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	expectInOrder(exec, "", "cmd-onCreate", "cmd-updateContent")
	if err := RunHooks(log.Null, exec, merged, RunOptions{SkipNonBlocking: true}); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}
}
