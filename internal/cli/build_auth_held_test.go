package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// isolateBuildAuth points the credential chain at an empty docker config, so
// only DEVCONTAINERS_OCI_AUTH can supply credentials.
func isolateBuildAuth(t *testing.T, ociAuth string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("DEVCONTAINERS_OCI_AUTH", ociAuth)
}

// A build whose registries have no credentials keeps its ambient auth: no
// DOCKER_CONFIG is handed to the build.
func TestBridgeBuildAuthWithoutCredentials(t *testing.T) {
	isolateBuildAuth(t, "")
	env, cleanup := bridgeBuildAuth(log.Null, "reg.example.com/base:1", nil, nil, "")
	defer cleanup()
	if env != nil {
		t.Errorf("env = %q, want nil when nothing resolves", env)
	}
}

// Credentials that resolve are bridged through a temporary DOCKER_CONFIG.
func TestBridgeBuildAuthWithCredentials(t *testing.T) {
	isolateBuildAuth(t, "reg.example.com|alice|secret")
	env, cleanup := bridgeBuildAuth(log.Null, "reg.example.com/base:1", nil, nil, "")
	defer cleanup()
	if len(env) != 1 {
		t.Fatalf("env = %q, want one DOCKER_CONFIG entry", env)
	}
	dir, ok := strings.CutPrefix(env[0], "DOCKER_CONFIG=")
	if !ok {
		t.Fatalf("env = %q, want DOCKER_CONFIG=<dir>", env)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Errorf("bridged config missing: %v", err)
	}
}
