package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/lifecycle"
)

// hostShell is a lifecycle.ContainerExecutor that runs each command with the
// host's sh, HOME set to home, so the marker scripts up runs in a container run
// for real. err, when set, is returned instead of running anything.
type hostShell struct {
	home     string
	err      error
	commands []string
}

func (h *hostShell) ExecInContainer(ctx context.Context, _, _ string, _ []string, command string) (string, string, int, error) {
	h.commands = append(h.commands, command)
	if h.err != nil {
		return "", "", 0, h.err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), "HOME="+h.home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), 0, err
}

func hostShellServer(t *testing.T, h *hostShell) *lifecycle.ShellServer {
	t.Helper()
	return lifecycle.NewShellServer(t.Context(), h, "c1", "")
}

// The markers live in the container user's $HOME/.devcontainer; a HOME that
// cannot be read, or is empty, leaves no folder.
func TestResolveContainerUserDataFolder(t *testing.T) {
	home := t.TempDir()
	if got := resolveContainerUserDataFolder(hostShellServer(t, &hostShell{home: home})); got != home+"/.devcontainer" {
		t.Errorf("folder = %q, want %q", got, home+"/.devcontainer")
	}
	if got := resolveContainerUserDataFolder(hostShellServer(t, &hostShell{})); got != "" {
		t.Errorf("folder = %q for an empty HOME, want none", got)
	}
	if got := resolveContainerUserDataFolder(hostShellServer(t, &hostShell{err: errors.New("exec failed")})); got != "" {
		t.Errorf("folder = %q when the exec failed, want none", got)
	}
}

// failingShell answers every command with exit code 1 and HOME's value on
// stdout, as a shell whose printf failed after writing would.
type failingShell struct{}

func (failingShell) ExecInContainer(context.Context, string, string, []string, string) (string, string, int, error) {
	return "/home/me", "", 1, nil
}

func TestResolveContainerUserDataFolderOfAFailedProbe(t *testing.T) {
	if got := resolveContainerUserDataFolder(lifecycle.NewShellServer(t.Context(), failingShell{}, "c1", "")); got != "" {
		t.Errorf("folder = %q when the probe exited 1, want none", got)
	}
}

// A hook runs once per timestamp: the first ask writes the marker and runs it,
// the same timestamp again does not, and a new one (a new container or start)
// runs it again. An unknown timestamp runs it without touching the marker.
func TestMarkerShouldRunOncePerTimestamp(t *testing.T) {
	folder := filepath.Join(t.TempDir(), ".devcontainer")
	h := &hostShell{home: t.TempDir()}
	server := hostShellServer(t, h)

	if !markerShouldRun(server, folder, "postCreateCommand", "2026-01-01T00:00:00Z") {
		t.Error("first run: want the hook to run")
	}
	marker, err := os.ReadFile(filepath.Join(folder, ".postCreateCommandMarker"))
	if err != nil || string(marker) != "2026-01-01T00:00:00Z\n" {
		t.Errorf("marker = %q, %v; want the timestamp", marker, err)
	}
	if markerShouldRun(server, folder, "postCreateCommand", "2026-01-01T00:00:00Z") {
		t.Error("same timestamp: want the hook skipped")
	}
	if !markerShouldRun(server, folder, "postCreateCommand", "2026-02-02T00:00:00Z") {
		t.Error("new timestamp: want the hook to run")
	}

	ran := len(h.commands)
	if !markerShouldRun(server, folder, "postStartCommand", "") {
		t.Error("unknown timestamp: want the hook to run")
	}
	if len(h.commands) != ran {
		t.Errorf("unknown timestamp ran %q in the container", h.commands[ran:])
	}
}

// A marker that cannot be checked does not run the hook.
func TestMarkerShouldRunWhenTheExecFails(t *testing.T) {
	server := hostShellServer(t, &hostShell{err: errors.New("exec failed")})
	if markerShouldRun(server, "/nowhere", "postCreateCommand", "ts") {
		t.Error("want the hook skipped when the marker exec failed")
	}
}
