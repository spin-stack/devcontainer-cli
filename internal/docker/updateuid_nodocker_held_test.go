package docker

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// buildContext returns the context path of a recorded `docker build`: its last
// argument.
func buildContext(t *testing.T, call recordedCall) string {
	t.Helper()
	if len(call.args) == 0 || call.args[0] != "build" {
		t.Fatalf("not a docker build: %v", call.args)
	}
	return call.args[len(call.args)-1]
}

// The UID image builds from the directory it writes updateUID.Dockerfile to
// unless a context directory is given, as TS updateRemoteUserUID builds in the
// folder holding its Dockerfile; a platform reaches the command line as
// --platform.
func TestBuildUIDImageContextAndPlatform(t *testing.T) {
	rr := &recordingRunner{}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	u := UIDUpdate{BaseImage: "base", FixedImage: "base-uid", RemoteUser: "vscode", ImageUser: "root", UID: 1000, GID: 1000}
	if err := BuildUIDImage(t.Context(), c, u); err != nil {
		t.Fatal(err)
	}
	call := rr.last()
	i := slices.Index(call.args, "-f")
	if i < 0 {
		t.Fatalf("no -f in %v", call.args)
	}
	if got, want := buildContext(t, call), filepath.Dir(call.args[i+1]); got != want || got == "" {
		t.Errorf("context = %q, want the Dockerfile's directory %q", got, want)
	}
	if slices.Contains(call.args, "--platform") {
		t.Errorf("no platform given, yet --platform in %v", call.args)
	}

	u.ContextDir = "/ctx"
	u.Platform = "linux/arm64"
	if err := BuildUIDImage(t.Context(), c, u); err != nil {
		t.Fatal(err)
	}
	call = rr.last()
	if got := buildContext(t, call); got != "/ctx" {
		t.Errorf("context = %q, want /ctx", got)
	}
	if i := slices.Index(call.args, "--platform"); i < 0 || call.args[i+1] != "linux/arm64" {
		t.Errorf("--platform linux/arm64 missing from %v", call.args)
	}
}
