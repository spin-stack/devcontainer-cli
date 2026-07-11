package oci

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestGetCredential_PriorityAndFormats(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		registry    string
		wantBasic   string
		wantRefresh string
	}{
		{
			name: "explicit auth wins over github token",
			env: map[string]string{
				"DEVCONTAINERS_OCI_AUTH": "other.io|x|y,ghcr.io|alice|secret",
				"GITHUB_TOKEN":           "github-secret",
			},
			registry: "ghcr.io", wantBasic: "alice:secret",
		},
		{
			name: "github token",
			env: map[string]string{
				"GITHUB_TOKEN":  "token",
				"DOCKER_CONFIG": t.TempDir(),
			},
			registry: "ghcr.io", wantBasic: "USERNAME:token",
		},
		{
			name: "github enterprise token is not sent to ghcr",
			env: map[string]string{
				"GITHUB_TOKEN": "token", "GITHUB_HOST": "github.example.com",
				"DOCKER_CONFIG": t.TempDir(),
			},
			registry: "ghcr.io",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if dir := tt.env["DOCKER_CONFIG"]; dir != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"unrelated.invalid":{}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cred := getCredential(tt.env, tt.registry, log.Null)
			if tt.wantBasic == "" && tt.wantRefresh == "" {
				if cred != nil {
					t.Fatalf("credential = %#v, want anonymous", cred)
				}
				return
			}
			if cred == nil {
				t.Fatal("credential is nil")
			}
			decoded, err := base64.StdEncoding.DecodeString(cred.base64Encoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(decoded) != tt.wantBasic || cred.refreshToken != tt.wantRefresh {
				t.Fatalf("basic=%q refresh=%q", decoded, cred.refreshToken)
			}
		})
	}
}

func TestGetCredentialFromDockerConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		wantBasic   string
		wantRefresh string
	}{
		{"basic", `{"auths":{"registry.example.com":{"auth":"dXNlcjpwYXNz"}}}`, "user:pass", ""},
		{"identity token", `{"auths":{"registry.example.com":{"identitytoken":"refresh"}}}`, "", "refresh"},
		{"invalid config", `{`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			cred := getCredentialFromDockerConfig(map[string]string{"DOCKER_CONFIG": dir}, "registry.example.com", log.Null)
			if tt.wantBasic == "" && tt.wantRefresh == "" {
				if cred != nil {
					t.Fatalf("credential = %#v, want nil", cred)
				}
				return
			}
			if cred == nil {
				t.Fatal("credential is nil")
			}
			decoded, err := base64.StdEncoding.DecodeString(cred.base64Encoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(decoded) != tt.wantBasic || cred.refreshToken != tt.wantRefresh {
				t.Fatalf("basic=%q refresh=%q", decoded, cred.refreshToken)
			}
		})
	}
}
