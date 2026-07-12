package lifecycle

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// fakeShell is a hermetic shellExec: it records every command and answers via a
// handler, so ProbeRemoteEnv can be exercised without a real container.
type fakeShell struct {
	calls   []string
	handler func(cmd string) (string, int, error)
}

func (f *fakeShell) Exec(cmd string) (string, int, error) {
	f.calls = append(f.calls, cmd)
	return f.handler(cmd)
}

func (f *fakeShell) called(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// captureLog records Write() lines so tests can assert on emitted diagnostics.
type captureLog struct {
	log.Logger
	lines []string
}

func (c *captureLog) Write(text string, _ ...log.Level) {
	c.lines = append(c.lines, text)
}

func (c *captureLog) has(substr string) bool {
	for _, l := range c.lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func newCaptureLog() *captureLog { return &captureLog{Logger: log.Null} }

func TestProbeRemoteEnv_None(t *testing.T) {
	fs := &fakeShell{handler: func(string) (string, int, error) {
		t.Fatal("Exec must not be called for ProbeNone")
		return "", 0, nil
	}}
	env, err := ProbeRemoteEnv(log.Null, fs, ProbeNone, "root")
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Errorf("expected empty env, got %v", env)
	}
	// Empty strategy behaves like ProbeNone.
	if env, _ = ProbeRemoteEnv(log.Null, fs, "", "root"); len(env) != 0 {
		t.Errorf("empty strategy: expected empty env, got %v", env)
	}
}

func TestProbeRemoteEnv_CacheHit(t *testing.T) {
	fs := &fakeShell{handler: func(cmd string) (string, int, error) {
		if strings.HasPrefix(cmd, "cat '") {
			// Serve a cached probe result.
			return `{"FOO":"bar","PATH":"/cached/bin"}`, 0, nil
		}
		t.Fatalf("unexpected command on cache hit: %q", cmd)
		return "", 0, nil
	}}
	env, err := ProbeRemoteEnv(log.Null, fs, ProbeLoginShell, "vscode", "/tmp/sess")
	if err != nil {
		t.Fatal(err)
	}
	if env["FOO"] != "bar" || env["PATH"] != "/cached/bin" {
		t.Errorf("cache not used: %v", env)
	}
	// A cache hit must short-circuit before probing the shell.
	if fs.called("timeout 10") || fs.called("getent passwd") {
		t.Errorf("cache hit still probed the shell: %v", fs.calls)
	}
	// The cache path must be namespaced by strategy.
	if !fs.called("env-loginShell.json") {
		t.Errorf("cache path not strategy-scoped: %v", fs.calls)
	}
}

func TestProbeRemoteEnv_TimeoutFallback(t *testing.T) {
	fs := &fakeShell{handler: func(cmd string) (string, int, error) {
		switch {
		case strings.Contains(cmd, "getent passwd"):
			return "/bin/bash\n", 0, nil
		case strings.HasPrefix(cmd, "timeout 10 "):
			// Simulate `timeout` killing a hung shell startup script.
			return "", 124, nil
		case cmd == "env":
			return "FROM_FALLBACK=1\nPATH=/usr/bin\n", 0, nil
		case strings.Contains(cmd, `printf %s "$PATH"`):
			return "/usr/bin:/bin", 0, nil
		}
		t.Fatalf("unexpected command: %q", cmd)
		return "", 0, nil
	}}
	lg := newCaptureLog()
	env, err := ProbeRemoteEnv(lg, fs, ProbeLoginInteractiveShell, "root")
	if err != nil {
		t.Fatal(err)
	}
	if env["FROM_FALLBACK"] != "1" {
		t.Errorf("did not fall back to plain env: %v", env)
	}
	// Root keeps every base PATH entry (no /sbin filtering here).
	if env["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH = %q, want /usr/bin:/bin", env["PATH"])
	}
	if !fs.called("timeout 10 /bin/bash -lic env") {
		t.Errorf("expected login-interactive probe command, got %v", fs.calls)
	}
	if !fs.called("env") {
		t.Errorf("expected fallback env command, got %v", fs.calls)
	}
	if !lg.has("timed out after 10s") {
		t.Errorf("timeout warning not logged: %v", lg.lines)
	}
}

func TestProbeRemoteEnv_PathMergeAndPwdDrop(t *testing.T) {
	fs := &fakeShell{handler: func(cmd string) (string, int, error) {
		switch {
		case strings.Contains(cmd, "getent passwd"):
			return "/bin/zsh\n", 0, nil
		case strings.HasPrefix(cmd, "timeout 10 "):
			return "PATH=/home/vscode/.local/bin:/usr/bin\nHOME=/home/vscode\nPWD=/somewhere\n", 0, nil
		case strings.Contains(cmd, `printf %s "$PATH"`):
			// Base image PATH includes sbin dirs a non-root user should not gain.
			return "/usr/local/sbin:/usr/local/bin:/usr/bin:/sbin", 0, nil
		case strings.HasPrefix(cmd, "mkdir -p"):
			return "", 0, nil // writeEnvCache
		case strings.HasPrefix(cmd, "cat '"):
			return "", 1, nil // cache miss
		}
		t.Fatalf("unexpected command: %q", cmd)
		return "", 0, nil
	}}
	env, err := ProbeRemoteEnv(log.Null, fs, ProbeLoginShell, "vscode", "/tmp/sess")
	if err != nil {
		t.Fatal(err)
	}
	// Base /usr/local/bin merged in; sbin dirs dropped for non-root; probed
	// entries kept in place.
	want := "/usr/local/bin:/home/vscode/.local/bin:/usr/bin"
	if env["PATH"] != want {
		t.Errorf("PATH = %q, want %q", env["PATH"], want)
	}
	// PWD is the shell's cwd, not meaningful for the merged remote env.
	if _, ok := env["PWD"]; ok {
		t.Errorf("PWD should be dropped, got %q", env["PWD"])
	}
	if env["HOME"] != "/home/vscode" {
		t.Errorf("HOME = %q", env["HOME"])
	}
	// Login (non-interactive) shell uses -lc.
	if !fs.called("timeout 10 /bin/zsh -lc env") {
		t.Errorf("expected login-shell probe command, got %v", fs.calls)
	}
	// A session folder triggers a cache write.
	if !fs.called("mkdir -p") {
		t.Errorf("expected cache write, got %v", fs.calls)
	}
}

func TestGetUserShell(t *testing.T) {
	// Resolves the login shell from /etc/passwd.
	fs := &fakeShell{handler: func(string) (string, int, error) { return "  /bin/fish \n", 0, nil }}
	if got := getUserShell(fs); got != "/bin/fish" {
		t.Errorf("got %q, want /bin/fish", got)
	}
	// Falls back to /bin/sh when getent yields nothing.
	fs = &fakeShell{handler: func(string) (string, int, error) { return "\n", 0, nil }}
	if got := getUserShell(fs); got != "/bin/sh" {
		t.Errorf("empty getent: got %q, want /bin/sh", got)
	}
	// Falls back on non-zero exit too.
	fs = &fakeShell{handler: func(string) (string, int, error) { return "/bin/bash", 1, nil }}
	if got := getUserShell(fs); got != "/bin/sh" {
		t.Errorf("failed getent: got %q, want /bin/sh", got)
	}
}

func TestReadEnvCache(t *testing.T) {
	// Malformed JSON is treated as a cache miss.
	fs := &fakeShell{handler: func(string) (string, int, error) { return "{not json", 0, nil }}
	if got := readEnvCache(fs, ProbeLoginShell, "/tmp/sess"); got != nil {
		t.Errorf("malformed cache should be nil, got %v", got)
	}
	// Missing file (non-zero exit) is a cache miss.
	fs = &fakeShell{handler: func(string) (string, int, error) { return "", 1, nil }}
	if got := readEnvCache(fs, ProbeLoginShell, "/tmp/sess"); got != nil {
		t.Errorf("missing cache should be nil, got %v", got)
	}
	// Valid JSON is decoded.
	fs = &fakeShell{handler: func(string) (string, int, error) { return `{"A":"1"}`, 0, nil }}
	if got := readEnvCache(fs, ProbeLoginShell, "/tmp/sess"); got["A"] != "1" {
		t.Errorf("valid cache: got %v", got)
	}
}
