package cli

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// A string mount from image metadata resolves ${devcontainerId} across the
// whole spec before it is parsed.
func TestMountFromMetadataStringSpec(t *testing.T) {
	m, err := mountFromMetadata("type=volume,source=cache-${devcontainerId},target=/cache", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != "cache-abc123" || m.Target != "/cache" || string(m.Type) != "volume" {
		t.Errorf("mount = %+v, want volume cache-abc123 -> /cache", m)
	}
}

// An object mount keeps the type it names and is a bind mount without one.
func TestMountFromMetadataObjectType(t *testing.T) {
	volume, err := mountFromMetadata(map[string]any{"type": "volume", "source": "v", "target": "/v"}, "id")
	if err != nil {
		t.Fatal(err)
	}
	if string(volume.Type) != "volume" {
		t.Errorf("type = %q, want volume", volume.Type)
	}
	bind, err := mountFromMetadata(map[string]any{"source": "/h", "target": "/c"}, "id")
	if err != nil {
		t.Fatal(err)
	}
	if string(bind.Type) != "bind" {
		t.Errorf("type = %q, want bind when the mount names none", bind.Type)
	}
}

// An empty string mount records nothing in the configuration's metadata entry;
// a non-empty one is kept as written.
func TestMetadataMountsSkipsEmptyStrings(t *testing.T) {
	var cfg config.DevContainer
	if err := json.Unmarshal([]byte(`{"mounts": ["", "source=/h,target=/c,type=bind"]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	got := metadataMounts(&cfg)
	if len(got) != 1 || got[0] != "source=/h,target=/c,type=bind" {
		t.Errorf("metadataMounts = %#v, want only the non-empty spec", got)
	}
}
