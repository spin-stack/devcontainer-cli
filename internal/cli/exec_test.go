package cli

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestExecExitCode_SignaledDerives128PlusN(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
		want   int
	}{
		{"SIGTERM", syscall.SIGTERM, 128 + int(syscall.SIGTERM)},
		{"SIGKILL", syscall.SIGKILL, 128 + int(syscall.SIGKILL)},
		{"SIGINT", syscall.SIGINT, 128 + int(syscall.SIGINT)},
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

			if got := exitErr.ExitCode(); got != -1 {
				t.Fatalf("precondition: expected ExitCode()==-1 for signaled child, got %d", got)
			}

			if got := execExitCode(exitErr); got != tc.want {
				t.Errorf("execExitCode() = %d, want %d (128+%d)", got, tc.want, int(tc.signal))
			}
		})
	}
}

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
