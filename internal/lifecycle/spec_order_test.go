package lifecycle

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

// TestSpecLifecycleOrder locks the specification's lifecycle command order
// (containers.dev/implementors/spec, "Lifecycle scripts"): the in-container
// finalization phases run onCreateCommand → updateContentCommand →
// postCreateCommand, then on start/attach postStartCommand → postAttachCommand.
func TestSpecLifecycleOrder(t *testing.T) {
	// RunHooks runs the in-container phases in exactly that order. Each phase has
	// a single command; the mock expects them in sequence.
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

// TestSpecWaitForGatesExecution locks the spec's waitFor semantics: with
// skipNonBlocking, execution stops after the waitFor phase (default
// updateContentCommand), so later phases do not run before the tool connects.
func TestSpecWaitForGatesExecution(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		WaitFor:               "updateContentCommand",
		OnCreateCommands:      []any{"cmd-onCreate"},
		UpdateContentCommands: []any{"cmd-updateContent"},
		PostCreateCommands:    []any{"cmd-postCreate"},
	}
	exec := NewMockCommandExecutor(gomock.NewController(t))
	expectInOrder(exec, "", "cmd-onCreate", "cmd-updateContent") // postCreate must not run
	if err := RunHooks(log.Null, exec, merged, RunOptions{SkipNonBlocking: true}); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}
}
