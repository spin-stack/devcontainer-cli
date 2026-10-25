package lifecycle

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// probeShell answers the env probe with probed and the base $PATH query with
// basePath.
func probeShell(t *testing.T, probed, basePath string) *fakeShell {
	return &fakeShell{handler: func(cmd string) (string, int, error) {
		switch {
		case strings.Contains(cmd, "getent passwd"):
			return "/bin/sh\n", 0, nil
		case strings.HasPrefix(cmd, "timeout 10 "):
			return probed, 0, nil
		case strings.Contains(cmd, `printf %s "$PATH"`):
			return basePath, 0, nil
		case strings.HasPrefix(cmd, "mkdir -p"), strings.HasPrefix(cmd, "cat '"):
			return "", 1, nil
		}
		t.Fatalf("unexpected command: %q", cmd)
		return "", 0, nil
	}}
}

// The container's sbin entries join the probed PATH for root, by name or as
// uid 0 (TS mergePaths(shellPath, containerPath, user === 'root' || user === '0')),
// and for no user, which the exec runs as root; never for another user.
func TestProbeRemoteEnvMergesSbinForRootOnly(t *testing.T) {
	for user, want := range map[string]string{
		"":       "/usr/sbin:/usr/bin",
		"root":   "/usr/sbin:/usr/bin",
		"0":      "/usr/sbin:/usr/bin",
		"vscode": "/usr/bin",
	} {
		env, err := ProbeRemoteEnv(log.Null, probeShell(t, "PATH=/usr/bin\n", "/usr/sbin:/usr/bin"), ProbeLoginShell, user, "")
		if err != nil {
			t.Fatal(err)
		}
		if env["PATH"] != want {
			t.Errorf("user %q: PATH = %q, want %q", user, env["PATH"], want)
		}
	}
}

// An empty container PATH merges nothing: TS mergePaths runs only when both
// PATHs are non-empty.
func TestProbeRemoteEnvKeepsThePathWhenTheContainerHasNone(t *testing.T) {
	env, err := ProbeRemoteEnv(log.Null, probeShell(t, "PATH=/usr/bin\n", "  "), ProbeLoginShell, "root", "")
	if err != nil {
		t.Fatal(err)
	}
	if env["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want /usr/bin", env["PATH"])
	}
}

// A probe that found a single variable is still cached in the session folder.
func TestProbeRemoteEnvCachesASingleVariable(t *testing.T) {
	fs := probeShell(t, "HOME=/root\n", "")
	env, err := ProbeRemoteEnv(log.Null, fs, ProbeLoginShell, "root", "/tmp/sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env["HOME"] != "/root" {
		t.Fatalf("env = %v, want only HOME", env)
	}
	if !fs.called("mkdir -p '/tmp/sess'") {
		t.Errorf("one probed variable was not cached: %v", fs.calls)
	}
}
