package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeContext(t *testing.T, configDir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDockerContextHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_CONTEXT", "")
	writeContext(t, dir, "remote", "tcp://10.0.0.5:2376")

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"remote"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDockerContextHost(); got != "tcp://10.0.0.5:2376" {
		t.Fatalf("via currentContext = %q, want tcp://10.0.0.5:2376", got)
	}

	writeContext(t, dir, "other", "unix:///run/other.sock")
	t.Setenv("DOCKER_CONTEXT", "other")
	if got := resolveDockerContextHost(); got != "unix:///run/other.sock" {
		t.Fatalf("via DOCKER_CONTEXT = %q, want unix:///run/other.sock", got)
	}
}

func TestResolveDockerContextHostDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_CONTEXT", "")

	if got := resolveDockerContextHost(); got != "" {
		t.Fatalf("no config = %q, want empty", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"default"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDockerContextHost(); got != "" {
		t.Fatalf("default context = %q, want empty", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"ghost"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDockerContextHost(); got != "" {
		t.Fatalf("missing meta = %q, want empty", got)
	}
}
