package oci

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func installHelper(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell helper is POSIX-only")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-credential-"+name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return name
}

func TestGetCredentialFromHelper_OutputBranches(t *testing.T) {
	t.Run("empty output", func(t *testing.T) {
		helper := installHelper(t, "emptyhelper", "printf ''")
		if cred := getCredentialFromHelper("reg.example.com", helper, log.Null); cred != nil {
			t.Fatalf("empty helper output should yield nil, got %#v", cred)
		}
	})

	t.Run("non-JSON output", func(t *testing.T) {
		helper := installHelper(t, "junkhelper", "printf 'not-json-at-all'")
		if cred := getCredentialFromHelper("reg.example.com", helper, log.Null); cred != nil {
			t.Fatalf("non-JSON helper output should yield nil, got %#v", cred)
		}
	})
}

func TestGetCredentialFromDockerConfig_CredsStore(t *testing.T) {
	helper := installHelper(t, "storehelper", `read reg; printf '{"Username":"su","Secret":"sp"}\n'`)

	dir := t.TempDir()
	config := `{"credsStore":"` + helper + `"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DOCKER_CONFIG": dir}

	cred := getCredentialFromDockerConfig(env, "any.example.com", log.Null)
	if cred == nil {
		t.Fatal("credsStore helper did not resolve a credential")
	}
	if cred.base64Encoded == "" {
		t.Fatalf("expected Basic credential from credsStore, got %#v", cred)
	}
}
