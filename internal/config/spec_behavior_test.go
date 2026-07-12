package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSpecConfigDiscoveryPrecedence locks the specification's config discovery
// order (containers.dev/implementors/spec, "config location"): a tool searches
// for devcontainer.json as .devcontainer/devcontainer.json first, then the
// top-level .devcontainer.json.
func TestSpecConfigDiscoveryPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, ".devcontainer", "devcontainer.json")
	root := filepath.Join(dir, ".devcontainer.json")
	if err := os.WriteFile(nested, []byte(`{"image":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte(`{"image":"y"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Both present → .devcontainer/devcontainer.json wins.
	if got := FindConfigFile(dir); got != nested {
		t.Errorf("both present: FindConfigFile = %q, want %q (.devcontainer/ precedence)", got, nested)
	}
	// Only the top-level .devcontainer.json.
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if got := FindConfigFile(dir); got != root {
		t.Errorf("root only: FindConfigFile = %q, want %q", got, root)
	}
	// Neither → not found.
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if got := FindConfigFile(dir); got != "" {
		t.Errorf("none present: FindConfigFile = %q, want empty", got)
	}
}

// TestSpecDevContainerIDDeterminism locks the spec's ${devcontainerId} contract:
// a stable identifier derived from the container's id-labels — the same label
// set always yields the same id (independent of map order), and different label
// sets yield different ids.
func TestSpecDevContainerIDDeterminism(t *testing.T) {
	a := map[string]string{
		"devcontainer.local_folder": "/x",
		"devcontainer.config_file":  "/x/.devcontainer/devcontainer.json",
	}
	// Same labels, different insertion order (Go maps iterate randomly).
	b := map[string]string{
		"devcontainer.config_file":  "/x/.devcontainer/devcontainer.json",
		"devcontainer.local_folder": "/x",
	}
	if ComputeDevContainerID(a) != ComputeDevContainerID(b) {
		t.Error("devcontainerId is not stable across equal label sets (must be order-independent)")
	}
	if ComputeDevContainerID(a) == ComputeDevContainerID(map[string]string{"devcontainer.local_folder": "/y"}) {
		t.Error("devcontainerId collided for different label sets")
	}
	if ComputeDevContainerID(a) == "" {
		t.Error("devcontainerId is empty")
	}
}
