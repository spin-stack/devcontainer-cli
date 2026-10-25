package doctor

import (
	"context"
	"io"
	"testing"
)

// nameRunner records the executable each docker call runs.
type nameRunner struct{ names []string }

func (n *nameRunner) Run(_ context.Context, _ io.Writer, name string, _ ...string) ([]byte, []byte, int, error) {
	n.names = append(n.names, name)
	return []byte("27.0.3\n"), nil, 0, nil
}

func TestChecksRunTheConfiguredDockerBinary(t *testing.T) {
	for _, tt := range []struct{ configured, want string }{
		{"", "docker"},
		{"/opt/bin/podman-docker", "/opt/bin/podman-docker"},
	} {
		nr := &nameRunner{}
		checkDockerDaemon(t.Context(), &Env{Runner: nr, DockerPath: tt.configured})
		if len(nr.names) != 1 || nr.names[0] != tt.want {
			t.Errorf("DockerPath %q ran %v, want [%s]", tt.configured, nr.names, tt.want)
		}
	}
}
