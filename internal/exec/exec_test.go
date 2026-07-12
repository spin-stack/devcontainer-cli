package exec

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is a real child-process endpoint used by OSRunner tests.
// It deliberately exercises the os/exec boundary (argv, environment, both
// streams, exit status and cancellation) instead of mocking CommandContext.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_HELPER") != "1" {
		return
	}

	args := helperArgs(os.Args)
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "echo":
		_, _ = os.Stdout.WriteString(strings.Join(args[1:], "|"))
		_, _ = os.Stderr.WriteString(os.Getenv("EXEC_HELPER_STDERR"))
	case "exit":
		os.Exit(17)
	case "wait":
		time.Sleep(time.Hour)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func helperArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func helperRunner(env ...string) OSRunner {
	return OSRunner{Env: append([]string{
		"GO_WANT_EXEC_HELPER=1",
	}, env...)}
}

func TestOSRunnerProcessContract(t *testing.T) {
	stdout, stderr, code, err := helperRunner("EXEC_HELPER_STDERR=warning").Run(
		t.Context(), os.Args[0], "-test.run=^TestHelperProcess$", "--", "echo", "one", "two",
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := string(stdout); got != "one|two" {
		t.Errorf("stdout = %q, want one|two", got)
	}
	if got := string(stderr); got != "warning" {
		t.Errorf("stderr = %q, want warning", got)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

func TestOSRunnerNonZeroIsExitCodeNotTransportError(t *testing.T) {
	_, _, code, err := helperRunner().Run(
		t.Context(), os.Args[0], "-test.run=^TestHelperProcess$", "--", "exit",
	)
	if err != nil {
		t.Fatalf("non-zero child returned transport error: %v", err)
	}
	if code != 17 {
		t.Fatalf("code = %d, want 17", code)
	}
}

func TestOSRunnerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, _, code, err := helperRunner().Run(
		ctx, os.Args[0], "-test.run=^TestHelperProcess$", "--", "wait",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if code != -1 {
		t.Fatalf("code = %d, want -1 for a process killed by its context", code)
	}
}

func TestOSRunnerMissingBinaryIsTransportError(t *testing.T) {
	_, _, code, err := (OSRunner{}).Run(t.Context(), "/definitely/not/a/devcontainer-test-binary")
	if err == nil {
		t.Fatal("missing binary returned nil error")
	}
	if code != -1 {
		t.Fatalf("code = %d, want -1", code)
	}
}
