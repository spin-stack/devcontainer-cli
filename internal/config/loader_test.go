package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfigFile_DevcontainerDir(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0644)

	got := FindConfigFile(dir)
	want := filepath.Join(dcDir, "devcontainer.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindConfigFile_RootDotFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0644)

	got := FindConfigFile(dir)
	want := filepath.Join(dir, ".devcontainer.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindConfigFile_PreferDevcontainerDir(t *testing.T) {
	dir := t.TempDir()
	// Both exist — .devcontainer/devcontainer.json should win
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"a"}`), 0644)
	os.WriteFile(filepath.Join(dir, ".devcontainer.json"), []byte(`{"image":"b"}`), 0644)

	got := FindConfigFile(dir)
	want := filepath.Join(dcDir, "devcontainer.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindConfigFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	got := FindConfigFile(dir)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestLoadDevContainerConfig_Image(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"image": "ubuntu:22.04",
		"remoteUser": "vscode",
		"features": {
			"ghcr.io/devcontainers/features/go:1": {}
		}
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Config.IsImageConfig() {
		t.Error("expected image config")
	}
	if result.Config.Image != "ubuntu:22.04" {
		t.Errorf("image = %q", result.Config.Image)
	}
	if result.Config.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q", result.Config.RemoteUser)
	}
	if len(result.Config.Features) != 1 {
		t.Errorf("features len = %d", len(result.Config.Features))
	}
}

func TestLoadDevContainerConfig_Dockerfile(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"build": {
			"dockerfile": "Dockerfile",
			"context": ".."
		}
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Config.IsDockerfileConfig() {
		t.Error("expected dockerfile config")
	}
	if result.Config.GetDockerfile() != "Dockerfile" {
		t.Errorf("dockerfile = %q", result.Config.GetDockerfile())
	}
}

func TestLoadDevContainerConfig_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "custom.json")
	os.WriteFile(configPath, []byte(`{"image": "alpine"}`), 0644)

	result, err := LoadDevContainerConfig(dir, configPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Image != "alpine" {
		t.Errorf("image = %q", result.Config.Image)
	}
}

func TestLoadDevContainerConfig_WorkspaceFolder(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"image": "ubuntu",
		"workspaceFolder": "/custom/workspace"
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceConfig.WorkspaceFolder != "/custom/workspace" {
		t.Errorf("workspaceFolder = %q", result.WorkspaceConfig.WorkspaceFolder)
	}
}

func TestLoadDevContainerConfig_JSONC(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		// This is a comment
		"image": "ubuntu",
		"features": {}, // trailing comma
	}`), 0644)

	result, err := LoadDevContainerConfig(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Image != "ubuntu" {
		t.Errorf("image = %q", result.Config.Image)
	}
}

func TestLoadDevContainerConfig_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadDevContainerConfig(dir, "", "")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestWorkspaceFromPath(t *testing.T) {
	w := WorkspaceFromPath("/home/user/project")
	if w.IsWorkspaceFile {
		t.Error("should not be workspace file")
	}
	if w.RootFolderPath != "/home/user/project" {
		t.Errorf("root = %q", w.RootFolderPath)
	}

	w2 := WorkspaceFromPath("/home/user/project.code-workspace")
	if !w2.IsWorkspaceFile {
		t.Error("should be workspace file")
	}
	if w2.RootFolderPath != "/home/user" {
		t.Errorf("root = %q", w2.RootFolderPath)
	}
}

func TestGetDockerComposeFilePaths_FromConfig(t *testing.T) {
	c := &DevContainerConfig{
		ConfigFilePath:    "/project/.devcontainer/devcontainer.json",
		DockerComposeFile: StringOrStrings{"docker-compose.yml"},
	}
	paths, err := GetDockerComposeFilePaths(c, map[string]string{}, "/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths len = %d", len(paths))
	}
	if !filepath.IsAbs(paths[0]) {
		t.Errorf("expected absolute path, got %q", paths[0])
	}
}

func TestGetDockerComposeFilePaths_FromEnv(t *testing.T) {
	c := &DevContainerConfig{ConfigFilePath: "/project/devcontainer.json"}
	env := map[string]string{"COMPOSE_FILE": "a.yml:b.yml"}
	paths, err := GetDockerComposeFilePaths(c, env, "/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths len = %d", len(paths))
	}
}

func TestGetDockerComposeFilePaths_Defaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("version: '3'"), 0644)

	c := &DevContainerConfig{ConfigFilePath: filepath.Join(dir, "devcontainer.json")}
	paths, err := GetDockerComposeFilePaths(c, map[string]string{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths len = %d", len(paths))
	}

	// Add override
	os.WriteFile(filepath.Join(dir, "docker-compose.override.yml"), []byte("version: '3'"), 0644)
	paths, err = GetDockerComposeFilePaths(c, map[string]string{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths len = %d, want 2 (with override)", len(paths))
	}
}

func TestLoadAllFixtures(t *testing.T) {
	fixturesDir := filepath.Join("..", "..", "src", "test", "configs")
	if _, err := os.Stat(fixturesDir); os.IsNotExist(err) {
		t.Skipf("fixtures dir not found: %s", fixturesDir)
	}

	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}

	var loaded, skipped int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(fixturesDir, entry.Name())
		configPath := FindConfigFile(dir)
		if configPath == "" {
			skipped++
			continue
		}

		result, err := LoadDevContainerConfig(dir, configPath, "")
		if err != nil {
			t.Errorf("FAIL %s: %v", entry.Name(), err)
			continue
		}

		// Verify variant detection is consistent
		variants := 0
		if result.Config.IsImageConfig() {
			variants++
		}
		if result.Config.IsDockerfileConfig() {
			variants++
		}
		if result.Config.IsComposeConfig() {
			variants++
		}
		if variants != 1 {
			t.Errorf("%s: detected %d variants (expected exactly 1)", entry.Name(), variants)
		}
		loaded++
	}
	t.Logf("loaded %d fixtures, skipped %d (no devcontainer.json)", loaded, skipped)
}
