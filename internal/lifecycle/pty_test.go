//go:build !windows

package lifecycle

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExecWithPTY_PropagatesExitCode(t *testing.T) {
	code, err := ExecWithPTY("/bin/sh", []string{"-c", "exit 7"})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestExecWithPTY_ReportsStartFailure(t *testing.T) {
	code, err := ExecWithPTY(filepath.Join(t.TempDir(), "missing"), nil)
	if err == nil || !strings.Contains(err.Error(), "start pty") {
		t.Fatalf("code=%d error=%v, want start failure", code, err)
	}
}
