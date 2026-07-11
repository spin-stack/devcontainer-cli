package oci

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// writeFakeHelper installs a `docker-credential-faketest` executable on PATH that
// implements the docker credential-helper `get` protocol: it reads the registry
// host from stdin and returns a JSON {Username,Secret}. The registry name selects
// the branch so a single script drives both the `<token>`→refreshToken path and
// the ordinary Basic-auth path. Returns the helper name.
func writeFakeHelper(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake shell helper is POSIX-only")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
read reg
case "$reg" in
  *token*) printf '{"Username":"<token>","Secret":"refresh-xyz"}\n' ;;
  *)       printf '{"Username":"u","Secret":"p"}\n' ;;
esac
`
	path := filepath.Join(dir, "docker-credential-faketest")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return "faketest"
}

// TestGetCredentialFromHelper_Protocol drives getCredentialFromHelper end-to-end
// against a real on-PATH helper binary, covering both the ordinary Basic-auth
// result and the previously-untested `<token>`→refreshToken branch.
func TestGetCredentialFromHelper_Protocol(t *testing.T) {
	helper := writeFakeHelper(t)

	t.Run("basic username/secret", func(t *testing.T) {
		cred := getCredentialFromHelper("basic.example.com", helper, log.Null)
		if cred == nil {
			t.Fatal("credential is nil")
		}
		if cred.refreshToken != "" {
			t.Fatalf("did not expect a refresh token, got %q", cred.refreshToken)
		}
		decoded, err := base64.StdEncoding.DecodeString(cred.base64Encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != "u:p" {
			t.Fatalf("basic = %q, want %q", decoded, "u:p")
		}
	})

	t.Run("<token> maps to refreshToken", func(t *testing.T) {
		cred := getCredentialFromHelper("token.example.com", helper, log.Null)
		if cred == nil {
			t.Fatal("credential is nil")
		}
		if cred.base64Encoded != "" {
			t.Fatalf("did not expect Basic auth for <token>, got %q", cred.base64Encoded)
		}
		if cred.refreshToken != "refresh-xyz" {
			t.Fatalf("refreshToken = %q, want %q", cred.refreshToken, "refresh-xyz")
		}
	})

	t.Run("missing helper binary returns nil", func(t *testing.T) {
		if cred := getCredentialFromHelper("x.example.com", "doesnotexist", log.Null); cred != nil {
			t.Fatalf("expected nil for missing helper, got %#v", cred)
		}
	})
}

// TestCredHelperViaDockerConfig proves the full wiring: a docker config.json that
// maps a registry to a credHelper resolves through getCredentialFromDockerConfig
// into the helper protocol (both branches).
func TestCredHelperViaDockerConfig(t *testing.T) {
	helper := writeFakeHelper(t)
	dir := t.TempDir()
	config := `{"credHelpers":{"token.example.com":"` + helper + `","basic.example.com":"` + helper + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DOCKER_CONFIG": dir}

	cred := getCredentialFromDockerConfig(env, "token.example.com", log.Null)
	if cred == nil || cred.refreshToken != "refresh-xyz" {
		t.Fatalf("credHelper token branch: got %#v", cred)
	}

	cred = getCredentialFromDockerConfig(env, "basic.example.com", log.Null)
	if cred == nil {
		t.Fatal("credHelper basic branch: nil credential")
	}
	decoded, _ := base64.StdEncoding.DecodeString(cred.base64Encoded)
	if string(decoded) != "u:p" {
		t.Fatalf("credHelper basic branch: basic = %q, want u:p", decoded)
	}
}

// TestDefaultCredentialHelperName pins the divergence from the TS CLI: on Linux
// the libsecret helper is `secretservice` (NOT the TS CLI's `secret`, which names
// no real binary). Go keeps `secretservice`.
func TestDefaultCredentialHelperName(t *testing.T) {
	if got := linuxDefaultHelperName(false); got != "secretservice" {
		t.Errorf("linux default without pass = %q, want secretservice", got)
	}
	if got := linuxDefaultHelperName(true); got != "pass" {
		t.Errorf("linux default with pass = %q, want pass", got)
	}
	// The wrong TS name must never be produced.
	for _, got := range []string{linuxDefaultHelperName(false), linuxDefaultHelperName(true)} {
		if got == "secret" {
			t.Errorf("must not use TS's incorrect helper name %q", got)
		}
	}
	if runtime.GOOS == "linux" {
		// On Linux the platform default resolves to pass or secretservice, never
		// "secret" and never an empty string.
		got := defaultCredentialHelperName()
		if got != "pass" && got != "secretservice" {
			t.Errorf("linux defaultCredentialHelperName() = %q, want pass|secretservice", got)
		}
	}
}
