package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfigFile(t *testing.T) {
	tests := []struct {
		name  string
		setup func(dir string)
		want  func(dir string) string
	}{
		{
			name: "devcontainer dir",
			setup: func(dir string) {
				dcDir := filepath.Join(dir, ".devcontainer")
				os.MkdirAll(dcDir, 0755)
				os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0644)
			},
			want: func(dir string) string {
				return filepath.Join(dir, ".devcontainer", "devcontainer.json")
			},
		},
		{
			name: "root dot file",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, ".devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0644)
			},
			want: func(dir string) string {
				return filepath.Join(dir, ".devcontainer.json")
			},
		},
		{
			name: "prefer devcontainer dir when both exist",
			setup: func(dir string) {
				dcDir := filepath.Join(dir, ".devcontainer")
				os.MkdirAll(dcDir, 0755)
				os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"a"}`), 0644)
				os.WriteFile(filepath.Join(dir, ".devcontainer.json"), []byte(`{"image":"b"}`), 0644)
			},
			want: func(dir string) string {
				return filepath.Join(dir, ".devcontainer", "devcontainer.json")
			},
		},
		{
			name:  "not found",
			setup: func(dir string) {},
			want:  func(dir string) string { return "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(dir)
			got := FindConfigFile(dir)
			if want := tt.want(dir); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestLoadDevContainerConfig(t *testing.T) {
	tests := []struct {
		name string
		// files maps a path relative to the temp dir to its content.
		files map[string]string
		// configPath and overridePath are relative to the temp dir; empty means "".
		configPath   string
		overridePath string
		wantErr      bool
		check        func(t *testing.T, result *LoadResult)
	}{
		{
			name: "image",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"image": "ubuntu:22.04",
					"remoteUser": "vscode",
					"features": {
						"ghcr.io/devcontainers/features/go:1": {}
					}
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
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
			},
		},
		{
			name: "dockerfile",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"build": {
						"dockerfile": "Dockerfile",
						"context": ".."
					}
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
				if !result.Config.IsDockerfileConfig() {
					t.Error("expected dockerfile config")
				}
				if result.Config.Dockerfile() != "Dockerfile" {
					t.Errorf("dockerfile = %q", result.Config.Dockerfile())
				}
			},
		},
		{
			name: "explicit path",
			files: map[string]string{
				"custom.json": `{"image": "alpine"}`,
			},
			configPath: "custom.json",
			check: func(t *testing.T, result *LoadResult) {
				if result.Config.Image != "alpine" {
					t.Errorf("image = %q", result.Config.Image)
				}
			},
		},
		{
			name: "workspace folder",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"image": "ubuntu",
					"workspaceFolder": "/custom/workspace"
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
				if result.WorkspaceConfig.WorkspaceFolder != "/custom/workspace" {
					t.Errorf("workspaceFolder = %q", result.WorkspaceConfig.WorkspaceFolder)
				}
			},
		},
		{
			name: "jsonc with comments and trailing comma",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					// This is a comment
					"image": "ubuntu",
					"features": {}, // trailing comma
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
				if result.Config.Image != "ubuntu" {
					t.Errorf("image = %q", result.Config.Image)
				}
			},
		},
		{
			name:    "not found",
			files:   map[string]string{},
			wantErr: true,
		},
		{
			name: "override path",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{"image": "original"}`,
				"override.json":                   `{"image": "overridden", "remoteUser": "custom"}`,
			},
			overridePath: "override.json",
			check: func(t *testing.T, result *LoadResult) {
				if result.Config.Image != "overridden" {
					t.Errorf("image = %q, want 'overridden'", result.Config.Image)
				}
				if result.Config.RemoteUser != "custom" {
					t.Errorf("remoteUser = %q", result.Config.RemoteUser)
				}
			},
		},
		{
			name: "lifecycle commands",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"image": "ubuntu",
					"postCreateCommand": "npm install",
					"onCreateCommand": ["sh", "-c", "echo hello"],
					"postStartCommand": {"a": "cmd1", "b": "cmd2"}
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
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
			},
		},
		{
			name: "features",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"image": "ubuntu",
					"features": {
						"ghcr.io/devcontainers/features/go:1": {"version": "1.21"},
						"ghcr.io/devcontainers/features/node:1": true,
						"./local-feature": {}
					}
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
				if len(result.Config.Features) != 3 {
					t.Errorf("features count = %d", len(result.Config.Features))
				}
			},
		},
		{
			name: "mounts",
			files: map[string]string{
				".devcontainer/devcontainer.json": `{
					"image": "ubuntu",
					"mounts": [
						"source=vol,target=/data,type=volume",
						{"type": "bind", "source": "/host", "target": "/container"}
					]
				}`,
			},
			check: func(t *testing.T, result *LoadResult) {
				if len(result.Config.Mounts) != 2 {
					t.Errorf("mounts = %d", len(result.Config.Mounts))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tt.files {
				full := filepath.Join(dir, rel)
				os.MkdirAll(filepath.Dir(full), 0755)
				os.WriteFile(full, []byte(content), 0644)
			}

			configPath := ""
			if tt.configPath != "" {
				configPath = filepath.Join(dir, tt.configPath)
			}
			overridePath := ""
			if tt.overridePath != "" {
				overridePath = filepath.Join(dir, tt.overridePath)
			}

			result, err := LoadDevContainerConfig(dir, configPath, overridePath)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error for missing config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, result)
		})
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
	c := &DevContainer{
		ConfigFilePath:    "/project/.devcontainer/devcontainer.json",
		DockerComposeFile: StringOrStrings{"docker-compose.yml"},
	}
	paths, err := DockerComposeFilePaths(c, map[string]string{}, "/project")
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
	c := &DevContainer{ConfigFilePath: "/project/devcontainer.json"}
	env := map[string]string{"COMPOSE_FILE": "a.yml:b.yml"}
	paths, err := DockerComposeFilePaths(c, env, "/project")
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

	c := &DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json")}
	paths, err := DockerComposeFilePaths(c, map[string]string{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths len = %d", len(paths))
	}

	// Add override
	os.WriteFile(filepath.Join(dir, "docker-compose.override.yml"), []byte("version: '3'"), 0644)
	paths, err = DockerComposeFilePaths(c, map[string]string{}, dir)
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
