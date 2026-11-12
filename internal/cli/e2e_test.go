package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func TestE2E_BuildUpExec_Image(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	fixture := filepath.Join("..", "..", "src", "test", "configs", "image")
	if _, err := os.Stat(fixture); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	buildOut := runCLI(t, "build", "--workspace-folder", fixture)
	assertOutcome(t, buildOut, "success")

	upOut := runCLI(t, "up", "--workspace-folder", fixture, "--skip-post-create")
	assertOutcome(t, upOut, "success")

	result := parseJSON(t, upOut)
	containerID, _ := result["containerId"].(string)
	if containerID == "" {
		t.Fatal("no containerId in up output")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	_ = cmd

	execCmd := exec.Command("docker", "exec", containerID, "echo", "e2e-test-ok")
	execOut, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\n%s", err, string(execOut))
	}
	if !strings.Contains(string(execOut), "e2e-test-ok") {
		t.Errorf("exec output = %q", string(execOut))
	}

	exec.Command("docker", "rm", "-f", containerID).Run()
}

func TestE2E_BuildUpExec_Dockerfile(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	fixture := filepath.Join("..", "..", "src", "test", "configs", "dockerfile-without-features")
	if _, err := os.Stat(fixture); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	buildOut := runCLI(t, "build", "--workspace-folder", fixture)
	assertOutcome(t, buildOut, "success")
	buildResult := parseJSON(t, buildOut)
	t.Logf("Built image: %v", buildResult["imageName"])

	upOut := runCLI(t, "up", "--workspace-folder", fixture, "--skip-post-create")
	assertOutcome(t, upOut, "success")

	result := parseJSON(t, upOut)
	containerID, _ := result["containerId"].(string)
	if containerID == "" {
		t.Fatal("no containerId")
	}

	inspectCmd := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", containerID)
	inspectOut, _ := inspectCmd.Output()
	if strings.TrimSpace(string(inspectOut)) != "true" {
		t.Errorf("container not running: %s", string(inspectOut))
	}

	exec.Command("docker", "rm", "-f", containerID).Run()
}

func TestE2E_ReadConfiguration_AllVariants(t *testing.T) {
	startFakeEngine(t, nil)
	fixtures := []struct {
		name    string
		checkFn func(t *testing.T, cfg map[string]any)
	}{
		{
			"image",
			func(t *testing.T, cfg map[string]any) {
				if cfg["image"] != "ubuntu:latest" {
					t.Errorf("image = %v", cfg["image"])
				}
			},
		},
		{
			"dockerfile-without-features",
			func(t *testing.T, cfg map[string]any) {
				build, ok := cfg["build"].(map[string]any)
				if !ok {
					t.Fatal("expected build property")
				}
				if build["dockerfile"] != "Dockerfile" {
					t.Errorf("dockerfile = %v", build["dockerfile"])
				}
			},
		},
		{
			"compose-image-without-features",
			func(t *testing.T, cfg map[string]any) {
				if cfg["service"] != "app" {
					t.Errorf("service = %v", cfg["service"])
				}
			},
		},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			fixture := filepath.Join("..", "..", "src", "test", "configs", tt.name)
			if _, err := os.Stat(fixture); os.IsNotExist(err) {
				t.Skip("fixture not found")
			}

			out := runCLI(t, "read-configuration", "--workspace-folder", fixture)
			result := parseJSON(t, out)
			cfg, ok := result["configuration"].(map[string]any)
			if !ok {
				t.Fatal("no configuration in output")
			}
			tt.checkFn(t, cfg)
		})
	}
}

func runCLI(t *testing.T, args ...string) string {
	t.Helper()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs(args)
	root.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return strings.TrimSpace(buf.String())
}

func parseJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nOutput: %s", err, s)
	}
	return result
}

func assertOutcome(t *testing.T, output, expected string) {
	t.Helper()
	result := parseJSON(t, output)
	if result["outcome"] != expected {
		t.Errorf("outcome = %v, want %q\nFull output: %s", result["outcome"], expected, output)
	}
}
