package config

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/jsonc"
)

func TestDevContainerConfig_ImageVariant(t *testing.T) {
	input := `{"image": "ubuntu:22.04", "remoteUser": "vscode"}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if !c.IsImageConfig() {
		t.Error("expected image config")
	}
	if c.IsDockerfileConfig() || c.IsComposeConfig() {
		t.Error("should not be dockerfile or compose")
	}
	if c.Image != "ubuntu:22.04" {
		t.Errorf("image = %q", c.Image)
	}
	if c.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q", c.RemoteUser)
	}
}

func TestDevContainerConfig_DockerfileVariant_Legacy(t *testing.T) {
	input := `{"dockerFile": "Dockerfile", "context": "."}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if !c.IsDockerfileConfig() {
		t.Error("expected dockerfile config")
	}
	if c.GetDockerfile() != "Dockerfile" {
		t.Errorf("dockerfile = %q", c.GetDockerfile())
	}
}

func TestDevContainerConfig_DockerfileVariant_Build(t *testing.T) {
	input := `{"build": {"dockerfile": "Dockerfile.dev", "target": "dev", "args": {"NODE_VERSION": "18"}}}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if !c.IsDockerfileConfig() {
		t.Error("expected dockerfile config")
	}
	if c.GetDockerfile() != "Dockerfile.dev" {
		t.Errorf("dockerfile = %q", c.GetDockerfile())
	}
	if c.Build.Target != "dev" {
		t.Errorf("target = %q", c.Build.Target)
	}
	if c.Build.Args["NODE_VERSION"] != "18" {
		t.Errorf("args = %v", c.Build.Args)
	}
}

func TestDevContainerConfig_ComposeVariant(t *testing.T) {
	input := `{"dockerComposeFile": "docker-compose.yml", "service": "app", "workspaceFolder": "/workspace"}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if !c.IsComposeConfig() {
		t.Error("expected compose config")
	}
	if c.Service != "app" {
		t.Errorf("service = %q", c.Service)
	}
	if len(c.DockerComposeFile) != 1 || c.DockerComposeFile[0] != "docker-compose.yml" {
		t.Errorf("dockerComposeFile = %v", c.DockerComposeFile)
	}
}

func TestDevContainerConfig_ComposeVariant_Array(t *testing.T) {
	input := `{"dockerComposeFile": ["docker-compose.yml", "docker-compose.override.yml"], "service": "app", "workspaceFolder": "/workspace"}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.DockerComposeFile) != 2 {
		t.Errorf("dockerComposeFile len = %d", len(c.DockerComposeFile))
	}
}

func TestLifecycleCommand_String(t *testing.T) {
	input := `{"postCreateCommand": "npm install"}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	s, ok := c.PostCreateCommand.AsString()
	if !ok || s != "npm install" {
		t.Errorf("postCreateCommand = %q, ok = %v", s, ok)
	}
}

func TestLifecycleCommand_Array(t *testing.T) {
	input := `{"postCreateCommand": ["npm", "install"]}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	arr, ok := c.PostCreateCommand.AsStringSlice()
	if !ok || len(arr) != 2 || arr[0] != "npm" || arr[1] != "install" {
		t.Errorf("postCreateCommand = %v, ok = %v", arr, ok)
	}
}

func TestLifecycleCommand_Map(t *testing.T) {
	input := `{"postCreateCommand": {"install": "npm install", "build": "npm run build"}}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	m, ok := c.PostCreateCommand.AsMap()
	if !ok {
		t.Fatal("expected map")
	}
	if m["install"] != "npm install" {
		t.Errorf("install = %v", m["install"])
	}
}

func TestMountOrString_String(t *testing.T) {
	input := `{"mounts": ["source=vol,target=/data,type=volume"]}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Mounts) != 1 {
		t.Fatalf("mounts len = %d", len(c.Mounts))
	}
	s, ok := c.Mounts[0].AsString()
	if !ok || s != "source=vol,target=/data,type=volume" {
		t.Errorf("mount = %q, ok = %v", s, ok)
	}
}

func TestMountOrString_Object(t *testing.T) {
	input := `{"mounts": [{"type": "volume", "source": "vol", "target": "/data"}]}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Mounts) != 1 {
		t.Fatalf("mounts len = %d", len(c.Mounts))
	}
	mt, ok := c.Mounts[0].AsMount()
	if !ok {
		t.Fatal("expected mount object")
	}
	if mt.Type != "volume" || mt.Source != "vol" || mt.Target != "/data" {
		t.Errorf("mount = %+v", mt)
	}
}

func TestStringOrStrings_Single(t *testing.T) {
	var s StringOrStrings
	if err := json.Unmarshal([]byte(`"file.yml"`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 || s[0] != "file.yml" {
		t.Errorf("got %v", s)
	}
}

func TestStringOrStrings_Array(t *testing.T) {
	var s StringOrStrings
	if err := json.Unmarshal([]byte(`["a.yml", "b.yml"]`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 {
		t.Errorf("got %v", s)
	}
}

func TestUpdateFromOldProperties(t *testing.T) {
	c := &DevContainerConfig{
		Extensions: []string{"ms-python.python"},
		DevPort:    intPtr(3000),
	}
	UpdateFromOldProperties(c)

	if len(c.Extensions) != 0 {
		t.Error("extensions should be cleared")
	}
	if c.DevPort != nil {
		t.Error("devPort should be cleared")
	}

	vscode, ok := c.Customizations["vscode"].(map[string]interface{})
	if !ok {
		t.Fatal("expected vscode customizations")
	}
	exts, ok := vscode["extensions"].([]interface{})
	if !ok || len(exts) != 1 || exts[0] != "ms-python.python" {
		t.Errorf("extensions = %v", vscode["extensions"])
	}
	if vscode["devPort"] != 3000 {
		t.Errorf("devPort = %v", vscode["devPort"])
	}
}

func TestFeatures(t *testing.T) {
	input := `{"image": "ubuntu", "features": {"ghcr.io/devcontainers/features/go:1": {"version": "1.21"}, "ghcr.io/devcontainers/features/node:1": true}}`
	var c DevContainerConfig
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Features) != 2 {
		t.Errorf("features len = %d", len(c.Features))
	}
}

func TestParseFixture_WithJSONC(t *testing.T) {
	// Simulates a JSONC fixture with comments
	input := []byte(`{
		// Dev container config
		"image": "mcr.microsoft.com/devcontainers/base:ubuntu",
		"features": {
			"ghcr.io/devcontainers/features/go:1": {} // Go feature
		},
		"postCreateCommand": "go version",
	}`)

	var c DevContainerConfig
	if err := jsonc.Unmarshal(input, &c); err != nil {
		t.Fatal(err)
	}
	if !c.IsImageConfig() {
		t.Error("expected image config")
	}
	if c.Image != "mcr.microsoft.com/devcontainers/base:ubuntu" {
		t.Errorf("image = %q", c.Image)
	}
	s, ok := c.PostCreateCommand.AsString()
	if !ok || s != "go version" {
		t.Errorf("postCreateCommand = %q", s)
	}
}

func intPtr(i int) *int { return &i }
