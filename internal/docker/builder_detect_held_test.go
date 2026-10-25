package docker

import (
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// A buildx command that exits non-zero reports nothing, even when it printed
// what would parse as a builder.
func TestBuilderDetectionIgnoresAFailedBuildx(t *testing.T) {
	inspect := &recordingRunner{responses: []cannedResponse{{stdout: []byte("Name: default\nDriver: docker\n"), code: 1}}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: inspect}
	if info := c.DetectActiveBuilder(t.Context()); info.Name != "" || info.Driver != "" {
		t.Errorf("DetectActiveBuilder after a failed inspect = %+v, want empty", info)
	}

	ls := &recordingRunner{responses: []cannedResponse{
		{stdout: []byte("default\n"), code: 0},
		{stdout: []byte("NAME DRIVER\ndefault docker running\n"), code: 1},
	}}
	c2 := &Client{DockerPath: "docker", Log: log.Null, Runner: ls}
	if got := c2.FindDockerDriverBuilder(t.Context()); got != "" {
		t.Errorf("FindDockerDriverBuilder after a failed ls = %q, want empty", got)
	}
}
