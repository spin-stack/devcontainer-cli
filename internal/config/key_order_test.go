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

func TestLoadKeepsContainerEnvAndFeatureOptionOrder(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "devcontainer.json")
	override := filepath.Join(dir, "override.json")
	if err := os.WriteFile(base, []byte(`{
		"image": "i",
		"containerEnv": {"ZED": "1", "ALPHA": "2"},
		"features": {"./f": {"zz": "1", "aa": "2"}, "./g": true}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`{"containerEnv": {"MID": "3", "ZED": "4"}, "features": {"./f": {"mm": "3"}, "./h": {"b": 1, "a": 2}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := LoadDevContainerConfig(dir, base, override)
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Config
	if want := []string{"ZED", "ALPHA", "MID", "ZED"}; !slices.Equal(cfg.ContainerEnvOrder, want) {
		t.Errorf("ContainerEnvOrder = %v, want %v", cfg.ContainerEnvOrder, want)
	}
	want := map[string][]string{"./f": {"zz", "aa", "mm"}, "./h": {"b", "a"}}
	if len(cfg.FeatureOptionOrder) != len(want) {
		t.Errorf("FeatureOptionOrder = %v, want %v", cfg.FeatureOptionOrder, want)
	}
	for id, names := range want {
		if !slices.Equal(cfg.FeatureOptionOrder[id], names) {
			t.Errorf("FeatureOptionOrder[%s] = %v, want %v", id, cfg.FeatureOptionOrder[id], names)
		}
	}
}
