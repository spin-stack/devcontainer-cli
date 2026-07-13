package docker

import "strings"

import "testing"

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
