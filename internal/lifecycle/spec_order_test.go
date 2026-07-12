package lifecycle

import (
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

// TestSpecLifecycleOrder locks the specification's lifecycle command order
// (containers.dev/implementors/spec, "Lifecycle scripts"): the in-container
// finalization phases run onCreateCommand → updateContentCommand →
// postCreateCommand, then on start/attach postStartCommand → postAttachCommand.
func TestSpecLifecycleOrder(t *testing.T) {
	// AllPhases advertises the canonical order, initializeCommand (host) first.
	wantPhases := []Phase{
		PhaseInitialize, PhaseOnCreate, PhaseUpdateContent,
		PhasePostCreate, PhasePostStart, PhasePostAttach,
	}
	if got := AllPhases(); !reflect.DeepEqual(got, wantPhases) {
		t.Fatalf("AllPhases() = %v, want %v", got, wantPhases)
	}

	// RunHooks runs the in-container phases in exactly that order. Each phase has
	// a single command; the recording executor captures the execution sequence.
	merged := &imagemeta.MergedConfig{
		OnCreateCommands:      []interface{}{"cmd-onCreate"},
		UpdateContentCommands: []interface{}{"cmd-updateContent"},
		PostCreateCommands:    []interface{}{"cmd-postCreate"},
		PostStartCommands:     []interface{}{"cmd-postStart"},
		PostAttachCommands:    []interface{}{"cmd-postAttach"},
	}
	exec := &mockExecutor{}
	if err := RunHooks(log.Null, exec, merged, RunOptions{}); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}

	want := []string{"cmd-onCreate", "cmd-updateContent", "cmd-postCreate", "cmd-postStart", "cmd-postAttach"}
	if !reflect.DeepEqual(exec.commands, want) {
		t.Fatalf("execution order = %v, want %v", exec.commands, want)
	}
}

// TestSpecWaitForGatesExecution locks the spec's waitFor semantics: with
// skipNonBlocking, execution stops after the waitFor phase (default
// updateContentCommand), so later phases do not run before the tool connects.
func TestSpecWaitForGatesExecution(t *testing.T) {
	merged := &imagemeta.MergedConfig{
		WaitFor:               "updateContentCommand",
		OnCreateCommands:      []interface{}{"cmd-onCreate"},
		UpdateContentCommands: []interface{}{"cmd-updateContent"},
		PostCreateCommands:    []interface{}{"cmd-postCreate"},
	}
	exec := &mockExecutor{}
	if err := RunHooks(log.Null, exec, merged, RunOptions{SkipNonBlocking: true}); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}
	want := []string{"cmd-onCreate", "cmd-updateContent"}
	if !reflect.DeepEqual(exec.commands, want) {
		t.Fatalf("with waitFor=updateContentCommand + skipNonBlocking, ran %v, want %v (postCreate must not run)", exec.commands, want)
	}
}
