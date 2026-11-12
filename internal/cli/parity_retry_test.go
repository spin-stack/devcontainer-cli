package cli

import (
	"context"
	"testing"
)

func TestIsRetryableFailure(t *testing.T) {
	cases := []struct {
		name          string
		stdout        string
		stderr        string
		wantRetryable bool
	}{
		{
			name:          "buildkit failed to solve (isInfraError)",
			stderr:        "ERROR: failed to solve: rpc error: code = Unknown desc = failed to fetch",
			wantRetryable: true,
		},
		{
			name:          "docker daemon unavailable (isInfraError)",
			stderr:        "Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
			wantRetryable: true,
		},
		{
			name:          "reference container-setup wrapper",
			stdout:        `{"description":"An error occurred setting up the container.","message":"Command failed: docker build -f /tmp/x -t vsc-parity-dotfiles --platform linux/amd64 /tmp/x","outcome":"error"}`,
			wantRetryable: true,
		},
		{
			name:          "config not found is a stable contract error",
			stdout:        `{"outcome":"error","message":"Dev container config (path/devcontainer.json) not found."}`,
			wantRetryable: false,
		},
		{
			name:          "flag validation is a stable contract error",
			stderr:        "Unknown argument: --bogus-flag",
			wantRetryable: false,
		},
		{
			name:          "empty output is not retryable",
			wantRetryable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableFailure(tc.stdout, tc.stderr); got != tc.wantRetryable {
				t.Errorf("isRetryableFailure(%q, %q) = %v, want %v", tc.stdout, tc.stderr, got, tc.wantRetryable)
			}
		})
	}
}

func TestRunWithInfraRetry(t *testing.T) {
	const transient = "ERROR: failed to solve: connection reset by peer"
	const stable = `{"outcome":"error","message":"Dev container config not found."}`

	t.Run("transient failure then success is absorbed", func(t *testing.T) {
		calls := 0
		_, _, exit := runWithInfraRetry(context.Background(), parityInfraRetries, func() (string, string, int) {
			calls++
			if calls == 1 {
				return "", transient, 1
			}
			return "ok", "", 0
		})
		if exit != 0 {
			t.Errorf("exit = %d, want 0 (flake should be absorbed)", exit)
		}
		if calls != 2 {
			t.Errorf("calls = %d, want 2 (one retry)", calls)
		}
	})

	t.Run("stable contract failure is not retried", func(t *testing.T) {
		calls := 0
		_, _, exit := runWithInfraRetry(context.Background(), parityInfraRetries, func() (string, string, int) {
			calls++
			return stable, "", 1
		})
		if exit != 1 {
			t.Errorf("exit = %d, want 1", exit)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (no retry for a stable contract error)", calls)
		}
	})

	t.Run("persistent transient failure surfaces after the cap", func(t *testing.T) {
		calls := 0
		_, stderr, exit := runWithInfraRetry(context.Background(), parityInfraRetries, func() (string, string, int) {
			calls++
			return "", transient, 1
		})
		if exit != 1 {
			t.Errorf("exit = %d, want 1 (real failure must still go red)", exit)
		}
		if calls != parityInfraRetries {
			t.Errorf("calls = %d, want %d (retry cap)", calls, parityInfraRetries)
		}
		if stderr != transient {
			t.Errorf("stderr = %q, want the final attempt's output", stderr)
		}
	})

	t.Run("cancelled context stops retries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		_, _, exit := runWithInfraRetry(ctx, parityInfraRetries, func() (string, string, int) {
			calls++
			return "", transient, 1
		})
		if exit != 1 {
			t.Errorf("exit = %d, want 1", exit)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (no retry once the deadline passed)", calls)
		}
	})
}
