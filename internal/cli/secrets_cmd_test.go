package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

var secretsCommands = []struct {
	name string
	run  func(t *testing.T, out *captureOutput, workspace, secretsFile string) error
}{
	{"up", func(t *testing.T, out *captureOutput, workspace, secretsFile string) error {
		return runUp(t.Context(), out, &upOpts{workspaceFolder: workspace, secretsFile: secretsFile, logLevel: "info", logFormat: "text", buildkit: "auto", gpuAvailability: "detect", workspaceMountConsistency: "cached", defaultUserEnvProbe: "loginInteractiveShell", updateRemoteUserUIDDefault: "on"})
	}},
	{"run-user-commands", func(t *testing.T, out *captureOutput, workspace, secretsFile string) error {
		opts := validRunUserCommandsOpts()
		opts.workspaceFolder, opts.secretsFile = workspace, secretsFile
		return runUserCommands(t.Context(), out, opts)
	}},
	{"build", func(t *testing.T, out *captureOutput, workspace, secretsFile string) error {
		return runBuild(t.Context(), out, &buildOpts{workspaceFolder: workspace, secretsFile: secretsFile, logLevel: "info", logFormat: "text", buildkit: "auto"})
	}},
}

func TestSecretsFileErrorFailsTheCommand(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/devcontainer-cli-test.sock")
	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, cmd := range secretsCommands {
		for _, tc := range []struct {
			name, path, message string
		}{
			{"missing", missing, "ENOENT: no such file or directory, open '" + missing + "'"},
			{"invalid json", writeSecretsFile(t, `{"A":"1",}`), "Invalid json data"},
		} {
			t.Run(cmd.name+"/"+tc.name, func(t *testing.T) {
				out := &captureOutput{}
				err := cmd.run(t, out, t.TempDir(), tc.path)
				if ece := asExitCode(t, err); ece.Code != 1 {
					t.Errorf("exit code = %d, want 1", ece.Code)
				}
				want := `{"outcome":"error","message":"` + tc.message + `","description":"Failed to read/parse secrets"}`
				if got := strings.TrimSpace(out.out.String()); got != want {
					t.Errorf("stdout = %s\nwant     %s", got, want)
				}
			})
		}
	}
}

func TestSecretsFileInJSONCGetsToTheConfiguration(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/devcontainer-cli-test.sock")
	secrets := writeSecretsFile(t, "{\n// comment\n\"A\": 1, \"B\": [1, 2], \"C\": {\"x\": true} /* end */\n}")
	for _, cmd := range secretsCommands {
		t.Run(cmd.name, func(t *testing.T) {
			out := &captureOutput{}
			err := cmd.run(t, out, t.TempDir(), secrets)
			if ece := asExitCode(t, err); ece.Code != 1 {
				t.Errorf("exit code = %d, want 1", ece.Code)
			}
			if got := out.out.String(); !strings.Contains(got, "Dev container config") || strings.Contains(got, "secrets") {
				t.Errorf("stdout = %q, want the configuration not found, past the secrets file", got)
			}
		})
	}
}

func TestSecretsFileNullValueFailsLikeTS(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/devcontainer-cli-test.sock")
	secrets := writeSecretsFile(t, `{"A":null}`)
	for _, tc := range []struct {
		command, description string
	}{
		{"up", "An error occurred setting up the container."},
		{"run-user-commands", "An error occurred running user commands in the container."},
	} {
		t.Run(tc.command, func(t *testing.T) {
			for _, cmd := range secretsCommands {
				if cmd.name != tc.command {
					continue
				}
				out := &captureOutput{}
				err := cmd.run(t, out, t.TempDir(), secrets)
				if ece := asExitCode(t, err); ece.Code != 1 {
					t.Errorf("exit code = %d, want 1", ece.Code)
				}
				want := `{"outcome":"error","message":"Cannot read properties of null (reading 'length')","description":"` + tc.description + `"}`
				if got := strings.TrimSpace(out.out.String()); got != want {
					t.Errorf("stdout = %s\nwant     %s", got, want)
				}
			}
		})
	}
}
