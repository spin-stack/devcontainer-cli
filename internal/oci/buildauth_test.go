package oci

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func readConfigAuths(t *testing.T, dir string) map[string]dockerConfigAuth {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var cf struct {
		Auths map[string]dockerConfigAuth `json:"auths"`
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cf.Auths
}

func TestResolveBuildAuthFromOCIEnvAndGitHubToken(t *testing.T) {
	env := map[string]string{
		"DEVCONTAINERS_OCI_AUTH": "myreg.example.com|user|s3cr3t",
		"GITHUB_TOKEN":           "ghtok",
	}
	dir, cleanup, ok, err := ResolveBuildAuth(env, []string{"myreg.example.com", "ghcr.io", "docker.io"}, log.Null)
	if err != nil || !ok {
		t.Fatalf("ResolveBuildAuth ok=%v err=%v", ok, err)
	}
	defer cleanup()

	auths := readConfigAuths(t, dir)
	// docker.io has no resolvable credential here → must be absent.
	if len(auths) != 2 {
		t.Fatalf("want 2 auths (myreg + ghcr), got %d: %v", len(auths), auths)
	}
	if got := auths["myreg.example.com"].Auth; got != base64.StdEncoding.EncodeToString([]byte("user:s3cr3t")) {
		t.Errorf("myreg auth = %q", got)
	}
	if got := auths["ghcr.io"].Auth; got != base64.StdEncoding.EncodeToString([]byte("USERNAME:ghtok")) {
		t.Errorf("ghcr auth = %q", got)
	}
	// The temp config must be self-contained: no credsStore/credHelpers keys.
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if s := string(data); strings.Contains(s, "credsStore") || strings.Contains(s, "credHelpers") {
		t.Errorf("temp config leaked store/helper keys:\n%s", s)
	}
}

func TestResolveBuildAuthNoCredsIsNoop(t *testing.T) {
	dir, cleanup, ok, err := ResolveBuildAuth(map[string]string{}, []string{"private.example.com", "docker.io"}, log.Null)
	defer cleanup()
	if ok || dir != "" || err != nil {
		t.Fatalf("expected no-op: ok=%v dir=%q err=%v", ok, dir, err)
	}
}

func TestResolveBuildAuthCleanupRemovesDir(t *testing.T) {
	env := map[string]string{"DEVCONTAINERS_OCI_AUTH": "reg.example.com|u|p"}
	dir, cleanup, ok, err := ResolveBuildAuth(env, []string{"reg.example.com"}, log.Null)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("dir should exist before cleanup: %v", statErr)
	}
	cleanup()
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("dir should be removed after cleanup, stat err=%v", statErr)
	}
}

func TestResolveBuildAuthDedupesRegistries(t *testing.T) {
	env := map[string]string{"DEVCONTAINERS_OCI_AUTH": "reg.example.com|u|p"}
	dir, cleanup, ok, _ := ResolveBuildAuth(env, []string{"reg.example.com", "reg.example.com", ""}, log.Null)
	if !ok {
		t.Fatal("expected ok")
	}
	defer cleanup()
	if auths := readConfigAuths(t, dir); len(auths) != 1 {
		t.Fatalf("want 1 deduped auth, got %d", len(auths))
	}
}

// TestResolveBuildAuth_IdentityToken covers the fix for token-only credentials
// (ACR / OAuth refresh token): they carry an identitytoken but no base64 auth, and
// were dropped by the old guard, leaving the build subprocess unauthenticated.
func TestResolveBuildAuth_IdentityToken(t *testing.T) {
	cfgDir := t.TempDir()
	cfg := `{"auths":{"myacr.azurecr.io":{"identitytoken":"eyJTOKEN"}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"DOCKER_CONFIG": cfgDir}
	dir, cleanup, ok, err := ResolveBuildAuth(env, []string{"myacr.azurecr.io"}, log.Null)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v — a token-only credential must still produce an auth entry", ok, err)
	}
	defer cleanup()

	entry, present := readConfigAuths(t, dir)["myacr.azurecr.io"]
	if !present {
		t.Fatal("identity-token registry missing from the build auth config")
	}
	if entry.IdentityToken != "eyJTOKEN" {
		t.Errorf("IdentityToken = %q, want eyJTOKEN", entry.IdentityToken)
	}
	if entry.Auth != "" {
		t.Errorf("Auth should be empty for a token-only credential, got %q", entry.Auth)
	}
}
