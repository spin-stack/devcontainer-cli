package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

	if got := FindConfigFile(dir); got != nested {
		t.Errorf("both present: FindConfigFile = %q, want %q (.devcontainer/ precedence)", got, nested)
	}
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if got := FindConfigFile(dir); got != root {
		t.Errorf("root only: FindConfigFile = %q, want %q", got, root)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if got := FindConfigFile(dir); got != "" {
		t.Errorf("none present: FindConfigFile = %q, want empty", got)
	}
}

func TestSpecDevContainerIDDeterminism(t *testing.T) {
	a := map[string]string{
		"devcontainer.local_folder": "/x",
		"devcontainer.config_file":  "/x/.devcontainer/devcontainer.json",
	}
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
