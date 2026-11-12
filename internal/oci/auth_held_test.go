package oci

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/log"
)

func pathOnly(t *testing.T, scripts map[string]string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the platform default helper is chosen on Linux; the fake helpers are POSIX shell")
	}
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func helperReturning(user string) string {
	return `read reg; printf '{"Username":"` + user + `","Secret":"s"}\n'`
}

func dockerConfigDir(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func basicUser(t *testing.T, cred *credential) string {
	t.Helper()
	if cred == nil {
		t.Fatal("credential is nil")
	}
	raw, err := base64.StdEncoding.DecodeString(cred.base64Encoded)
	if err != nil {
		t.Fatal(err)
	}
	user, _, _ := strings.Cut(string(raw), ":")
	return user
}

func TestPlatformDefaultHelperFollowsPass(t *testing.T) {
	for _, tt := range []struct {
		name     string
		withPass bool
		want     string
	}{
		{"pass on PATH", true, "pass"},
		{"no pass on PATH", false, "secretservice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scripts := map[string]string{
				"docker-credential-pass":          helperReturning("pass"),
				"docker-credential-secretservice": helperReturning("secretservice"),
			}
			if tt.withPass {
				scripts["pass"] = "exit 0"
			}
			pathOnly(t, scripts)
			env := map[string]string{"DOCKER_CONFIG": dockerConfigDir(t, "")}
			if got := basicUser(t, getCredentialFromDockerConfig(env, "reg.io", log.Null)); got != tt.want {
				t.Fatalf("credential from helper %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerConfigWithAuthSkipsPlatformDefault(t *testing.T) {
	for _, tt := range []struct {
		name, config string
		wantDefault  bool
	}{
		{"empty config", `{}`, true},
		{"a helper for another registry", `{"credHelpers":{"other.io":"x"}}`, false},
		{"a credsStore that finds nothing", `{"credsStore":"missing"}`, false},
		{"auths for another registry", `{"auths":{"other.io":{}}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pathOnly(t, map[string]string{"docker-credential-secretservice": helperReturning("default")})
			env := map[string]string{"DOCKER_CONFIG": dockerConfigDir(t, tt.config)}
			cred := getCredentialFromDockerConfig(env, "reg.io", log.Null)
			if !tt.wantDefault {
				if cred != nil {
					t.Fatalf("credential = %#v, want none", cred)
				}
				return
			}
			if got := basicUser(t, cred); got != "default" {
				t.Fatalf("credential from %q, want the platform default helper", got)
			}
		})
	}
}

func TestRegistryHelperDoesNotFallBackToCredsStore(t *testing.T) {
	pathOnly(t, map[string]string{
		"docker-credential-store": helperReturning("store"),
		"docker-credential-fails": "exit 1",
	})
	failing := map[string]string{"DOCKER_CONFIG": dockerConfigDir(t, `{"credHelpers":{"reg.io":"fails"},"credsStore":"store"}`)}
	if cred := getCredentialFromDockerConfig(failing, "reg.io", log.Null); cred != nil {
		t.Fatalf("credential = %#v, want none once the registry's helper failed", cred)
	}
	empty := map[string]string{"DOCKER_CONFIG": dockerConfigDir(t, `{"credHelpers":{"reg.io":""},"credsStore":"store"}`)}
	if got := basicUser(t, getCredentialFromDockerConfig(empty, "reg.io", log.Null)); got != "store" {
		t.Fatalf("credential from %q, want credsStore", got)
	}
}

// TS getCredentialFromHelper: a failing helper is a trace line, an empty answer
// is no credential without a word, and anything that is not JSON is a warning.
// Go also warns when a helper times out, which a helper that just fails must
// not claim.
func TestCredentialHelperOutcomesLog(t *testing.T) {
	pathOnly(t, map[string]string{
		"docker-credential-fails":  "exit 1",
		"docker-credential-silent": "exit 0",
		"docker-credential-junk":   "printf x",
	})
	for _, tt := range []struct {
		helper, want, notWant string
	}{
		{"fails", `Credential helper "fails" failed for "reg.io"`, "timed out"},
		{"silent", "", "non-JSON"},
		{"junk", `Credential helper "junk" returned non-JSON for "reg.io"`, ""},
	} {
		t.Run(tt.helper, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.New(log.Options{Writer: &buf, Level: log.LevelTrace})
			if cred := getCredentialFromHelper("reg.io", tt.helper, logger); cred != nil {
				t.Fatalf("credential = %#v, want none", cred)
			}
			if tt.want != "" && !strings.Contains(buf.String(), tt.want) {
				t.Errorf("log %q lacks %q", buf.String(), tt.want)
			}
			if tt.notWant != "" && strings.Contains(buf.String(), tt.notWant) {
				t.Errorf("log %q has %q", buf.String(), tt.notWant)
			}
		})
	}
}

func TestCredentialHelperThatHangsTimesOut(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	pathOnly(t, map[string]string{"docker-credential-hangs": "exec " + sleep + " 60"})
	var buf bytes.Buffer
	logger := log.New(log.Options{Writer: &buf, Level: log.LevelWarning})
	start := time.Now()
	if cred := getCredentialFromHelper("reg.io", "hangs", logger); cred != nil {
		t.Fatalf("credential = %#v, want none", cred)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Second || elapsed > 30*time.Second {
		t.Errorf("helper given up after %s, want 15s", elapsed)
	}
	if want := `Credential helper "hangs" timed out after 15s for "reg.io"`; !strings.Contains(buf.String(), want) {
		t.Errorf("warning log %q lacks %q", buf.String(), want)
	}
}
