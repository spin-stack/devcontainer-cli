package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/docker"
)

// composeWorkspace writes a workspace named proj whose devcontainer.json runs
// the app service of a Compose project.
func composeWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "proj")
	dir := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"devcontainer.json":  `{"dockerComposeFile":"docker-compose.yml","service":"app"}`,
		"docker-compose.yml": "services:\n  app:\n    image: alpine\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func fakeEngineClient(t *testing.T) *docker.EngineClient {
	t.Helper()
	engine, err := docker.NewEngineClient(quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine
}

// exec finds the workspace's container by its local_folder label first, and
// only then looks for the service of its Compose project.
func TestFindContainerByOptsPrefersTheLabeledContainer(t *testing.T) {
	ws := composeWorkspace(t)
	startFakeEngine(t, map[string]map[string]string{
		"labeled": {"devcontainer.local_folder": ws},
	})
	opts := &execOpts{workspaceFolder: ws}
	if got := findContainerByOpts(t.Context(), fakeEngineClient(t), opts, "", quietLogger()); got != "labeled" {
		t.Errorf("container = %q, want labeled", got)
	}
}

// A Compose workspace's container without devcontainer labels is found by the
// project (the workspace folder's name, or COMPOSE_PROJECT_NAME) and service.
func TestFindContainerByOptsFindsTheComposeService(t *testing.T) {
	ws := composeWorkspace(t)
	startFakeEngine(t, map[string]map[string]string{
		"app":    {"com.docker.compose.project": "proj_devcontainer", "com.docker.compose.service": "app"},
		"custom": {"com.docker.compose.project": "custom", "com.docker.compose.service": "app"},
		"db":     {"com.docker.compose.project": "proj_devcontainer", "com.docker.compose.service": "db"},
	})
	engine := fakeEngineClient(t)
	opts := &execOpts{workspaceFolder: ws}
	if got := findContainerByOpts(t.Context(), engine, opts, "", quietLogger()); got != "app" {
		t.Errorf("container = %q, want app", got)
	}
	t.Setenv("COMPOSE_PROJECT_NAME", "Custom")
	if got := findContainerByOpts(t.Context(), engine, opts, "", quietLogger()); got != "custom" {
		t.Errorf("container with COMPOSE_PROJECT_NAME = %q, want custom", got)
	}
}

// No Compose container is found without a workspace, without a config, for a
// config that is not Compose, or when the project has no such service.
func TestFindComposeContainerFindsNothing(t *testing.T) {
	startFakeEngine(t, map[string]map[string]string{
		"app": {"com.docker.compose.project": "proj_devcontainer", "com.docker.compose.service": "app"},
		"img": {"com.docker.compose.project": "img_devcontainer", "com.docker.compose.service": ""},
	})
	engine := fakeEngineClient(t)

	compose := composeWorkspace(t)
	t.Chdir(compose)
	if got := findComposeContainer(t.Context(), engine, &execOpts{}, quietLogger()); got != "" {
		t.Errorf("container without a workspace = %q, want none", got)
	}

	if got := findComposeContainer(t.Context(), engine, &execOpts{workspaceFolder: t.TempDir()}, quietLogger()); got != "" {
		t.Errorf("container without a config = %q, want none", got)
	}

	img := filepath.Join(t.TempDir(), "img")
	if err := os.MkdirAll(filepath.Join(img, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(img, ".devcontainer", "devcontainer.json"), []byte(`{"image":"alpine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findComposeContainer(t.Context(), engine, &execOpts{workspaceFolder: img}, quietLogger()); got != "" {
		t.Errorf("container of an image config = %q, want none", got)
	}

	t.Setenv("COMPOSE_PROJECT_NAME", "empty")
	if got := findComposeContainer(t.Context(), engine, &execOpts{workspaceFolder: compose}, quietLogger()); got != "" {
		t.Errorf("container of a project without the service = %q, want none", got)
	}
}
