package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

// RW-012 cross-layer error propagation.
//
// These tests exercise the command runners (build/up/exec) and assert that a
// hermetically-reachable pre-Docker error — argument validation or a config-load
// failure — propagates with the right exit code BEFORE any Docker engine/daemon
// is constructed. They inject nothing Docker-related: the runner must return
// early. Asserting on the SPECIFIC pre-Docker message (never "Docker engine")
// proves the runner short-circuited before docker.NewEngineClient, regardless of
// whether Docker happens to be installed on the host.
//
// NOTE (out of scope, per track brief): context cancellation cannot be tested
// here because the commands do not thread cmd.Context() through a cancellation
// seam into the pre-Docker phase; wiring that is a separate change and is
// intentionally not done in this track.

// asExitCode unwraps an *errors.ExitCodeError, failing if err is not one.
func asExitCode(t *testing.T, err error) *coreerrors.ExitCodeError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ece *coreerrors.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("error %v (%T) is not an *ExitCodeError", err, err)
	}
	return ece
}

func TestRunBuild_InvalidEnum_PreDocker(t *testing.T) {
	out := &captureOutput{}
	err := runBuild(context.Background(), out, &buildOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "bogus", // fails validateEnum before any Docker work
		logFormat:       "text",
		buildkit:        "auto",
	})

	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	// yargs-style validation writes to stderr and leaves stdout empty.
	if !strings.Contains(out.err.String(), "log-level") {
		t.Errorf("stderr = %q, want the invalid --log-level message", out.err.String())
	}
	if out.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty on a validation error", out.out.String())
	}
	if strings.Contains(err.Error(), "Docker engine") {
		t.Error("reached Docker engine construction; validation must short-circuit first")
	}
}

func TestRunBuild_ConfigLoadError_PreDocker(t *testing.T) {
	// An empty workspace has no devcontainer.json → config load fails, and that
	// failure is reported (JSON envelope on stdout, exit 1) before the engine is
	// built.
	out := &captureOutput{}
	err := runBuild(context.Background(), out, &buildOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "info",
		logFormat:       "text",
		buildkit:        "auto",
	})

	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	if !strings.Contains(out.out.String(), `"outcome":"error"`) {
		t.Errorf("stdout = %q, want an error JSON envelope", out.out.String())
	}
	if strings.Contains(err.Error(), "Docker engine") {
		t.Error("reached Docker engine construction; config load must short-circuit first")
	}
}

func TestRunUp_InvalidMount_PreDocker(t *testing.T) {
	out := &captureOutput{}
	err := runUp(context.Background(), out, &upOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "info",
		logFormat:       "text",
		buildkit:        "auto",
		gpuAvailability: "detect",
		mounts:          []string{"this-is-not-a-valid-mount-spec"},
	})

	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	if !strings.Contains(out.err.String(), "mount must match") {
		t.Errorf("stderr = %q, want the mount-format validation message", out.err.String())
	}
	if strings.Contains(err.Error(), "Docker engine") {
		t.Error("reached Docker engine construction; mount validation must short-circuit first")
	}
}

func TestRunUp_InvalidEnum_PreDocker(t *testing.T) {
	out := &captureOutput{}
	err := runUp(context.Background(), out, &upOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "info",
		logFormat:       "text",
		buildkit:        "sideways", // not in {auto, never}
		gpuAvailability: "detect",
	})

	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	if !strings.Contains(out.err.String(), "buildkit") {
		t.Errorf("stderr = %q, want the invalid --buildkit message", out.err.String())
	}
}

func TestRunExec_InvalidEnum_PreDocker(t *testing.T) {
	// runExec returns validation errors as plain errors (the top-level command
	// maps them to exit 1). The point is they surface before the engine.
	err := runExec(context.Background(), &execOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "verbose", // not in {info, debug, trace}
		logFormat:       "text",
	}, []string{"echo", "hi"})

	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "log-level") {
		t.Errorf("error = %v, want the invalid --log-level message", err)
	}
	if strings.Contains(err.Error(), "Docker engine") {
		t.Error("reached Docker engine construction; validation must short-circuit first")
	}
}

func TestRunExec_ConfigLoadError_PreDocker(t *testing.T) {
	// With a workspace folder and no --container-id, an unloadable config is
	// returned before the engine is constructed.
	err := runExec(context.Background(), &execOpts{
		workspaceFolder: t.TempDir(), // empty: no devcontainer.json
		logLevel:        "info",
		logFormat:       "text",
	}, []string{"echo", "hi"})

	if err == nil {
		t.Fatal("expected a config-load error, got nil")
	}
	if strings.Contains(err.Error(), "Docker engine") {
		t.Error("reached Docker engine construction; config load must short-circuit first")
	}
}
