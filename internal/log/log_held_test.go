package log

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func plainStdout(t *testing.T) {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = null
	t.Cleanup(func() {
		os.Stdout = saved
		_ = null.Close()
	})
}

func TestNewWritesToStderrByDefault(t *testing.T) {
	plainStdout(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved })

	New(Options{}).Write("to stderr", LevelInfo)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if !strings.HasSuffix(string(out), "] to stderr\n") {
		t.Errorf("stderr = %q, want the line", out)
	}
}

func TestNewWritesThePlainLogWhenStdoutIsNotATerminal(t *testing.T) {
	plainStdout(t)
	var buf bytes.Buffer
	New(Options{Writer: &buf}).Write("x", LevelInfo)
	if !regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z\] x\n$`).MatchString(buf.String()) {
		t.Errorf("output = %q, want the plain log line", buf.String())
	}
}

func TestNewDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Write("hidden", LevelDebug)
	l.Write("shown", LevelInfo)
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Errorf("output = %q, want only the info line", out)
	}
}

func TestBannerLineNamesTheKernelRelease(t *testing.T) {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Skipf("no kernel release to compare: %v", err)
	}
	want := fmt.Sprintf("@devcontainers/cli 1.2.3. Go %s. %s %s %s.", runtime.Version(), runtime.GOOS, strings.TrimSpace(string(b)), runtime.GOARCH)
	if got := BannerLine("1.2.3"); got != want {
		t.Errorf("BannerLine = %q, want %q", got, want)
	}
}
