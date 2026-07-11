package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildCommand_HelpOutput verifies the build command has expected flags.
func TestBuildCommand_HelpOutput(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"build", "--help"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.Execute()
	out := buf.String()

	flags := []string{"--workspace-folder", "--no-cache", "--image-name", "--platform", "--push", "--buildkit"}
	for _, f := range flags {
		if !strings.Contains(out, f) {
			t.Errorf("build help missing flag %q", f)
		}
	}
}

// readConfigurationResult runs `read-configuration` against a fixture and returns
// the parsed JSON output (skipping when the fixture is absent).
func readConfigurationResult(t *testing.T, fixtureName string) map[string]interface{} {
	t.Helper()
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", fixtureName)
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixture not found: " + fixtureName)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"read-configuration", "--workspace-folder", fixtureDir})
	root.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result); err != nil {
		t.Fatalf("invalid JSON for %s: %v", fixtureName, err)
	}
	return result
}

func TestReadConfiguration(t *testing.T) {
	// A compose config must expose its service; a Dockerfile config must expose
	// dockerFile or build (and a build target when the fixture has one).
	composeService := func(service string) func(*testing.T, map[string]interface{}) {
		return func(t *testing.T, result map[string]interface{}) {
			cfg, _ := result["configuration"].(map[string]interface{})
			if cfg["service"] != service {
				t.Errorf("service = %v, want %q", cfg["service"], service)
			}
		}
	}
	dockerfile := func(hasTarget bool) func(*testing.T, map[string]interface{}) {
		return func(t *testing.T, result map[string]interface{}) {
			cfg, _ := result["configuration"].(map[string]interface{})
			_, hasDockerFile := cfg["dockerFile"]
			build, hasBuild := cfg["build"].(map[string]interface{})
			if !hasDockerFile && !hasBuild {
				t.Error("expected dockerFile or build property")
			}
			if hasTarget && hasBuild {
				if build["target"] == nil || build["target"] == "" {
					t.Error("expected build target")
				}
			}
		}
	}

	tests := []struct {
		name    string
		fixture string
		check   func(t *testing.T, result map[string]interface{})
	}{
		{"with features", "image-with-features", func(t *testing.T, result map[string]interface{}) {
			cfg, _ := result["configuration"].(map[string]interface{})
			features, _ := cfg["features"].(map[string]interface{})
			if len(features) == 0 {
				t.Error("expected features in configuration")
			}
		}},
		{"variable substitution", "image", func(t *testing.T, result map[string]interface{}) {
			cfg, _ := result["configuration"].(map[string]interface{})
			remoteEnv, _ := cfg["remoteEnv"].(map[string]interface{})
			localPath, ok := remoteEnv["LOCAL_PATH"]
			if !ok {
				t.Skip("LOCAL_PATH not in remoteEnv")
			}
			pathStr, ok := localPath.(string)
			if !ok || pathStr == "" {
				t.Error("LOCAL_PATH should be resolved (not empty)")
			}
			if strings.Contains(pathStr, "${") {
				t.Errorf("LOCAL_PATH should not contain unresolved variables: %q", pathStr)
			}
		}},
		{"compose image", "compose-image-without-features", composeService("app")},
		{"compose dockerfile", "compose-Dockerfile-without-features", composeService("app")},
		{"compose with name", "compose-with-name", composeService("app")},
		{"dockerfile without features", "dockerfile-without-features", dockerfile(false)},
		{"dockerfile with features", "dockerfile-with-features", dockerfile(false)},
		{"dockerfile with target", "dockerfile-with-target", dockerfile(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, readConfigurationResult(t, tt.fixture))
		})
	}
}
