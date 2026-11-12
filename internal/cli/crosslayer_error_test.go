package cli

import (
	"errors"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

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
	err := runBuild(t.Context(), out, &buildOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "bogus",
		logFormat:       "text",
		buildkit:        "auto",
	})

	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
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
	out := &captureOutput{}
	err := runBuild(t.Context(), out, &buildOpts{
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
	err := runUp(t.Context(), out, &upOpts{
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
	err := runUp(t.Context(), out, &upOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "info",
		logFormat:       "text",
		buildkit:        "sideways",
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
	err := runExec(t.Context(), &execOpts{
		workspaceFolder: t.TempDir(),
		logLevel:        "verbose",
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
	err := runExec(t.Context(), &execOpts{
		workspaceFolder: t.TempDir(),
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
