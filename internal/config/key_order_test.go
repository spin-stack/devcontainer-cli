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
	if want := []string{"./f", "./g", "./h"}; !slices.Equal(cfg.FeatureOrder, want) {
		t.Errorf("FeatureOrder = %v, want %v", cfg.FeatureOrder, want)
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

func TestLoadKeepsBuildArgsOrder(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "devcontainer.json")
	override := filepath.Join(dir, "override.json")
	if err := os.WriteFile(base, []byte(`{
		// comment
		"build": {"dockerfile": "Dockerfile", "args": {"ZED": "1", "10": "a", "ALPHA": "2"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`{"build": {"args": {"MID": "3", "ZED": "4"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := LoadDevContainerConfig(dir, base, override)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ZED", "10", "ALPHA", "MID", "ZED"}; !slices.Equal(res.Config.BuildArgsOrder, want) {
		t.Errorf("BuildArgsOrder = %v, want %v", res.Config.BuildArgsOrder, want)
	}
}

func TestLoadOrdersFeatureIDsAsJavaScriptEnumeratesThem(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "devcontainer.json")
	override := filepath.Join(dir, "override.json")
	if err := os.WriteFile(base, []byte(`{"image": "i", "features": {"./zeta": {}, "10": {}, "./alpha": {}, "2": {}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`{"features": {"./beta": {}, "./zeta": {}, "1": {}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := LoadDevContainerConfig(dir, base, override)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1", "2", "10", "./zeta", "./alpha", "./beta"}; !slices.Equal(res.Config.FeatureOrder, want) {
		t.Errorf("FeatureOrder = %v, want %v", res.Config.FeatureOrder, want)
	}
}
