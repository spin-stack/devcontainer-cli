package cli

import "testing"

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
