package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadKeepsTheDocumentKeyOrder(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "devcontainer.json")
	override := filepath.Join(dir, "override.json")
	if err := os.WriteFile(base, []byte(`{"name": "n", "image": "i", "capAdd": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`{"remoteUser": "u", "name": "o"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := LoadDevContainerConfig(dir, base, override)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"name", "image", "capAdd", "remoteUser", "name"}; !slices.Equal(res.KeyOrder, want) {
		t.Errorf("KeyOrder = %v, want %v", res.KeyOrder, want)
	}
}
