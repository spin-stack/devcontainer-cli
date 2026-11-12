package docker

import (
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestDetectActiveBuilderKeepsTheBuildersOwnNameAndDriver(t *testing.T) {
	out := "Name:   mybuilder\nDriver: docker-container\n\nNodes:\nName:   mybuilder0\nDriver: other\nEndpoint: unix:///var/run/docker.sock\n"
	rr := &recordingRunner{responses: []cannedResponse{{stdout: []byte(out)}}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	info := c.DetectActiveBuilder(t.Context())
	if info.Name != "mybuilder" || info.Driver != "docker-container" {
		t.Errorf("DetectActiveBuilder = %+v, want {mybuilder docker-container}", info)
	}
}
