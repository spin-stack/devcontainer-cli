package cli

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// In detect mode the GPU is enabled only when `docker info` ran and reported
// the nvidia runtime; a failed docker info or one without the runtime ("<no
// value>") leaves it off.
func TestCheckGPUAvailabilityDetectsTheNvidiaRuntime(t *testing.T) {
	orig := nvidiaContainerCLIInfo
	t.Cleanup(func() { nvidiaContainerCLIInfo = orig })
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return false, false }

	for _, tc := range []struct {
		name   string
		stdout string
		err    error
		want   bool
	}{
		{"runtime reported", "nvidia-container-runtime\n", nil, true},
		{"no runtime", "<no value>\n", nil, false},
		{"docker info failed", "", errors.New("no docker"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeDockerRunner{reply: func([]string) (string, int, error) { return tc.stdout, 0, tc.err }}
			if got := checkGPUAvailability(t.Context(), "detect", fakeDockerClient(r)); got != tc.want {
				t.Errorf("checkGPUAvailability = %v, want %v", got, tc.want)
			}
			want := dockerCall{"info", "-f", "{{.Runtimes.nvidia}}"}
			if len(r.calls) != 1 || !slices.Equal(r.calls[0], want) {
				t.Errorf("docker calls = %v, want [%v]", r.calls, want)
			}
		})
	}
}
