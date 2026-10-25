package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeepMergeConfig(t *testing.T) {
	base := map[string]any{
		"image":      "ubuntu",
		"remoteUser": "vscode",
		"runArgs":    []any{"--a"},
		"customizations": map[string]any{
			"vscode": map[string]any{"extensions": []any{"x"}},
		},
	}
	override := map[string]any{
		"remoteUser": "root",
		"runArgs":    []any{"--b"}, // arrays replace, not append
		"customizations": map[string]any{
			"vscode": map[string]any{"settings": map[string]any{"k": "v"}},
		},
	}
	got := deepMergeConfig(base, override)

	if got["image"] != "ubuntu" {
		t.Errorf("base-only key lost: image=%v", got["image"])
	}
	if got["remoteUser"] != "root" {
		t.Errorf("scalar override failed: remoteUser=%v", got["remoteUser"])
	}
	if !reflect.DeepEqual(got["runArgs"], []any{"--b"}) {
		t.Errorf("array should be replaced, got %v", got["runArgs"])
	}
	// Nested object merged: extensions (base) preserved AND settings (override) added.
	vscode := got["customizations"].(map[string]any)["vscode"].(map[string]any)
	if !reflect.DeepEqual(vscode["extensions"], []any{"x"}) {
		t.Errorf("nested base key lost: extensions=%v", vscode["extensions"])
	}
	if vscode["settings"].(map[string]any)["k"] != "v" {
		t.Errorf("nested override key missing: settings=%v", vscode["settings"])
	}
}

func TestDeepMergeConfigNilHandling(t *testing.T) {
	m := map[string]any{"a": 1}
	if got := deepMergeConfig(nil, m); !reflect.DeepEqual(got, m) {
		t.Errorf("nil base should return override, got %v", got)
	}
	if got := deepMergeConfig(m, nil); !reflect.DeepEqual(got, m) {
		t.Errorf("nil override should return base, got %v", got)
	}
}

func TestLoadDevContainerConfigMergesOverride(t *testing.T) {
	ws := t.TempDir()
	dcDir := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := `{"image":"ubuntu:22.04","remoteUser":"vscode","runArgs":["--base"]}`
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(ws, "override.json")
	if err := os.WriteFile(overridePath, []byte(`{"remoteUser":"root"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := LoadDevContainerConfig(ws, "", overridePath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Deep-merge (Go divergence): base image/runArgs survive, override wins remoteUser.
	if result.Config.Image != "ubuntu:22.04" {
		t.Errorf("base image lost after override merge: %q", result.Config.Image)
	}
	if result.Config.RemoteUser != "root" {
		t.Errorf("override remoteUser not applied: %q", result.Config.RemoteUser)
	}
	if len(result.Config.RunArgs) != 1 || result.Config.RunArgs[0] != "--base" {
		t.Errorf("base runArgs lost: %v", result.Config.RunArgs)
	}
}
