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

// TestReadConfiguration_WithFeatures verifies features are present in output.
func TestReadConfiguration_WithFeatures(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", "image-with-features")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
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
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result)

	cfg, _ := result["configuration"].(map[string]interface{})
	features, _ := cfg["features"].(map[string]interface{})

	if len(features) == 0 {
		t.Error("expected features in configuration")
	}
}

// TestReadConfiguration_VariableSubstitution verifies that host env vars are resolved.
func TestReadConfiguration_VariableSubstitution(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", "image")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
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
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result)

	cfg, _ := result["configuration"].(map[string]interface{})
	remoteEnv, _ := cfg["remoteEnv"].(map[string]interface{})

	// LOCAL_PATH should be resolved from ${localEnv:PATH}
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
}

// TestReadConfiguration_ComposeVariant verifies compose configs parse correctly.
func TestReadConfiguration_ComposeVariant(t *testing.T) {
	fixtures := []struct {
		name    string
		service string
	}{
		{"compose-image-without-features", "app"},
		{"compose-Dockerfile-without-features", "app"},
		{"compose-with-name", "app"},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			fixtureDir := filepath.Join("..", "..", "src", "test", "configs", tt.name)
			if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
				t.Skip("fixture not found")
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
				t.Fatalf("invalid JSON: %v", err)
			}

			cfg, _ := result["configuration"].(map[string]interface{})
			if cfg["service"] != tt.service {
				t.Errorf("service = %v, want %q", cfg["service"], tt.service)
			}
		})
	}
}

// TestReadConfiguration_DockerfileVariants verifies different Dockerfile forms.
func TestReadConfiguration_DockerfileVariants(t *testing.T) {
	fixtures := []struct {
		name      string
		hasTarget bool
	}{
		{"dockerfile-without-features", false},
		{"dockerfile-with-features", false},
		{"dockerfile-with-target", true},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			fixtureDir := filepath.Join("..", "..", "src", "test", "configs", tt.name)
			if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
				t.Skip("fixture not found")
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
			json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result)

			cfg, _ := result["configuration"].(map[string]interface{})

			// Should have either dockerFile or build.dockerfile
			_, hasDockerFile := cfg["dockerFile"]
			build, hasBuild := cfg["build"].(map[string]interface{})

			if !hasDockerFile && !hasBuild {
				t.Error("expected dockerFile or build property")
			}

			if tt.hasTarget && hasBuild {
				if build["target"] == nil || build["target"] == "" {
					t.Error("expected build target")
				}
			}
		})
	}
}
