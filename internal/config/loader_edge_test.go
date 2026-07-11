package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDevContainerConfig_OverridePath(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image": "original"}`), 0644)

	overridePath := filepath.Join(dir, "override.json")
	os.WriteFile(overridePath, []byte(`{"image": "overridden", "remoteUser": "custom"}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", overridePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Image != "overridden" {
		t.Errorf("image = %q, want 'overridden'", result.Config.Image)
	}
	if result.Config.RemoteUser != "custom" {
		t.Errorf("remoteUser = %q", result.Config.RemoteUser)
	}
}

func TestLoadDevContainerConfig_LifecycleCommands(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"image": "ubuntu",
		"postCreateCommand": "npm install",
		"onCreateCommand": ["sh", "-c", "echo hello"],
		"postStartCommand": {"a": "cmd1", "b": "cmd2"}
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	s, ok := result.Config.PostCreateCommand.AsString()
	if !ok || s != "npm install" {
		t.Errorf("postCreateCommand = %v", result.Config.PostCreateCommand.Raw())
	}

	arr, ok := result.Config.OnCreateCommand.AsStringSlice()
	if !ok || len(arr) != 3 {
		t.Errorf("onCreateCommand = %v", result.Config.OnCreateCommand.Raw())
	}

	m, ok := result.Config.PostStartCommand.AsMap()
	if !ok || len(m) != 2 {
		t.Errorf("postStartCommand = %v", result.Config.PostStartCommand.Raw())
	}
}

func TestLoadDevContainerConfig_Features(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"image": "ubuntu",
		"features": {
			"ghcr.io/devcontainers/features/go:1": {"version": "1.21"},
			"ghcr.io/devcontainers/features/node:1": true,
			"./local-feature": {}
		}
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Config.Features) != 3 {
		t.Errorf("features count = %d", len(result.Config.Features))
	}
}

func TestLoadDevContainerConfig_Mounts(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"image": "ubuntu",
		"mounts": [
			"source=vol,target=/data,type=volume",
			{"type": "bind", "source": "/host", "target": "/container"}
		]
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Config.Mounts) != 2 {
		t.Errorf("mounts = %d", len(result.Config.Mounts))
	}
}

func TestDevContainerConfig_JSONRoundtrip(t *testing.T) {
	input := `{"image":"ubuntu","features":{"go:1":{}},"postCreateCommand":"echo hi"}`
	var cfg DevContainerConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var cfg2 DevContainerConfig
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if cfg2.Image != "ubuntu" {
		t.Errorf("image = %q after roundtrip", cfg2.Image)
	}
}
