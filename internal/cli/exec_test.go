package cli

import (
	"os/exec"
	"syscall"
	"testing"
)

// TestExecExitCode_SignaledDerives128PlusN verifies the 128+N contract for the
// edge where the host process (the CLI's `docker` child) is terminated by a
// signal. os/exec reports ExitCode()==-1 in that case; execExitCode must
// recover the signal from the WaitStatus and return 128+N, matching the TS CLI
// (e.g. 143 for SIGTERM), instead of surfacing -1/255.
//
// Hermetic: runs /bin/sleep only, no Docker. The test signals the child
// directly and then Waits, producing a real *exec.ExitError with a signaled
// WaitStatus.
func TestExecExitCode_SignaledDerives128PlusN(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
		want   int
	}{
		{"SIGTERM", syscall.SIGTERM, 128 + int(syscall.SIGTERM)}, // 143
		{"SIGKILL", syscall.SIGKILL, 128 + int(syscall.SIGKILL)}, // 137
		{"SIGINT", syscall.SIGINT, 128 + int(syscall.SIGINT)},    // 130
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sleep", "60")
			if err := cmd.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal: %v", err)
			}

			err := cmd.Wait()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("expected *exec.ExitError from a signaled process, got %T: %v", err, err)
			}

			// Sanity: os/exec reports -1 for a signaled child.
			if got := exitErr.ExitCode(); got != -1 {
				t.Fatalf("precondition: expected ExitCode()==-1 for signaled child, got %d", got)
			}

			if got := execExitCode(exitErr); got != tc.want {
				t.Errorf("execExitCode() = %d, want %d (128+%d)", got, tc.want, int(tc.signal))
			}
		})
	}
}

// TestExecExitCode_NormalExitPassthrough verifies that a normal (non-signaled)
// exit code is passed through unchanged, i.e. the signal-derivation path only
// triggers on ExitCode()==-1.
func TestExecExitCode_NormalExitPassthrough(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}

	if got := execExitCode(exitErr); got != 7 {
		t.Errorf("execExitCode() = %d, want 7", got)
	}
}
