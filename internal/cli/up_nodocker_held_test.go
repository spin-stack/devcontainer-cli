package cli

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

// The remote user of the UID update comes from the image's metadata label too:
// a root image whose metadata names a non-root remoteUser gets the -uid image,
// built for that user.
func TestSingleContainerRemoteUserUIDReadsTheImageMetadata(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the UID update runs on Linux only")
	}
	engine := &fakeEngineAPI{images: map[string]string{
		"img:1": `{"Config":{"User":"root","Labels":{"devcontainer.metadata":"[{\"remoteUser\":\"node\"}]"}}}`,
	}}
	runner := &fakeDockerRunner{}
	r := &upRunner{
		log:    log.Null,
		docker: fakeDockerClient(runner),
		engine: &docker.EngineClient{API: engine, Log: log.Null},
		opts:   &upOpts{updateRemoteUserUIDDefault: "on", userDataFolder: t.TempDir()},
	}
	got, err := r.singleContainerRemoteUserUID(t.Context(), "img:1", &config.DevContainer{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "-uid") {
		t.Errorf("image = %q, want the -uid image", got)
	}
	if len(runner.calls) != 1 || !slices.Contains(runner.calls[0], "REMOTE_USER=node") {
		t.Errorf("docker calls = %v, want one UID build for node", runner.calls)
	}
}
