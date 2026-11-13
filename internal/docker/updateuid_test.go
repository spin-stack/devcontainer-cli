package docker

import (
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// TestUpdateUIDDockerfileArgHasDefault guards against reintroducing the
// InvalidDefaultArgInFrom BuildKit warning: the ARG used by the FROM must carry
// a default value (the real base image is passed via --build-arg). Upstream #1242.
func TestUpdateUIDDockerfileArgHasDefault(t *testing.T) {
	if !strings.Contains(updateUIDDockerfile, "ARG BASE_IMAGE=") {
		t.Errorf("updateUID.Dockerfile must give ARG BASE_IMAGE a default to avoid InvalidDefaultArgInFrom")
	}
	if !strings.Contains(updateUIDDockerfile, "FROM $BASE_IMAGE") {
		t.Errorf("updateUID.Dockerfile should still FROM $BASE_IMAGE")
	}
}

func TestBuildUIDImageBuildArgOrder(t *testing.T) {
	rr := &recordingRunner{}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	err := BuildUIDImage(t.Context(), c, UIDUpdate{BaseImage: "base", FixedImage: "fixed", RemoteUser: "dev", ImageUser: "root", UID: 1000, GID: 1001, ContextDir: "/ctx"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	args := rr.last().args
	for i, a := range args {
		if a == "--build-arg" && i+1 < len(args) {
			got = append(got, args[i+1])
		}
	}
	want := []string{"BASE_IMAGE=base", "REMOTE_USER=dev", "NEW_UID=1000", "NEW_GID=1001", "IMAGE_USER=root"}
	if !slices.Equal(got, want) {
		t.Errorf("build args = %v, want %v", got, want)
	}
}
