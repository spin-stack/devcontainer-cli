package cli

import (
	"io"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

// An image whose inspection has no Config carries no metadata: the UID update
// reads none and, with updates off, keeps the image.
func TestSingleContainerRemoteUserUIDImageWithoutConfig(t *testing.T) {
	logger := log.New(log.Options{Writer: io.Discard})
	engine := &fakeEngineAPI{images: map[string]string{"img:1": `{}`}}
	r := &upRunner{
		log:    logger,
		engine: &docker.EngineClient{API: engine, Log: logger},
		opts:   &upOpts{updateRemoteUserUIDDefault: "off"},
	}
	got, err := r.singleContainerRemoteUserUID(t.Context(), "img:1", &config.DevContainer{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "img:1" {
		t.Errorf("image = %q, want img:1 kept", got)
	}
}
