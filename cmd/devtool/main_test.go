package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain runs the command itself, instead of the tests, in the child process
// TestTheCommandIsWired starts.
func TestMain(m *testing.M) {
	if os.Getenv("DEVTOOL_RUN_MAIN") == "1" {
		main()
	}
	os.Exit(m.Run())
}

// devtool runs this test binary as the devtool command, with args and stdin.
func devtool(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	// GOCOVERDIR keeps a binary built with coverage from warning on stderr that it
	// has nowhere to write its counters.
	cmd.Env = append(os.Environ(), "DEVTOOL_RUN_MAIN=1", "GOCOVERDIR="+t.TempDir())
	cmd.Stdin = strings.NewReader(stdin)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), o.String(), e.String()
}

// TestTheCommandIsWired holds what main adds to devtools.Main: the process's
// arguments, standard streams and exit code.
func TestTheCommandIsWired(t *testing.T) {
	if code, out, errOut := devtool(t, "internal/cli/exec.go\n", "parity-affected"); code != 0 || out != "exec\n" || errOut != "" {
		t.Errorf("parity-affected: code=%d stdout=%q stderr=%q, want 0 \"exec\\n\" \"\"", code, out, errOut)
	}
	if code, out, errOut := devtool(t, "", "nope"); code != 2 || out != "" || errOut != "devtool: unknown command \"nope\"\n" {
		t.Errorf("unknown command: code=%d stdout=%q stderr=%q, want 2", code, out, errOut)
	}
}
