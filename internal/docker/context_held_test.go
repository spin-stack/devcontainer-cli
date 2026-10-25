package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// "default" is the Docker CLI's built-in context: its endpoint comes from the
// environment, never from the context store, even if a meta.json exists for it.
func TestResolveDockerContextHostIgnoresAStoredDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_CONTEXT", "default")
	writeContext(t, dir, "default", "tcp://10.0.0.9:2376")
	if got := resolveDockerContextHost(); got != "" {
		t.Errorf("host = %q, want empty for the default context", got)
	}
}

// A config.json without currentContext selects "default".
func TestActiveContextNameDefaultsWithoutCurrentContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONTEXT", "")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := activeContextName(dir); got != "default" {
		t.Errorf("activeContextName = %q, want default", got)
	}
}

// Without DOCKER_CONFIG the store is ~/.docker, as for the Docker CLI.
func TestResolveDockerContextHostReadsTheHomeStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("DOCKER_CONTEXT", "")
	dir := filepath.Join(home, ".docker")
	writeContext(t, dir, "remote", "tcp://10.0.0.7:2376")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"remote"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDockerContextHost(); got != "tcp://10.0.0.7:2376" {
		t.Errorf("host = %q, want tcp://10.0.0.7:2376 from ~/.docker", got)
	}
}
