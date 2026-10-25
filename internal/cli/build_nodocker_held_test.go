package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build whose image or Dockerfile step fails before any Docker call reports
// the failure on stdout and exits 1, never the success envelope: a legacy
// Feature id as the bare message, any other failure as a container error.
func TestRunBuildReportsAFailedBuildStep(t *testing.T) {
	for name, tc := range map[string]struct {
		config string
		want   string
	}{
		"legacy feature":     {`{"image":"alpine:3","features":{"not-a-known-legacy-feature":{}}}`, `"message":"Legacy feature 'not-a-known-legacy-feature' not supported.`},
		"missing dockerfile": {`{"build":{"dockerfile":"missing.Dockerfile"}}`, "An error occurred building the container"},
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, ".devcontainer.json"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			out := &captureOutput{}
			err := runBuild(t.Context(), out, &buildOpts{
				workspaceFolder: ws,
				dockerPath:      filepath.Join(t.TempDir(), "no-docker"),
				logLevel:        "info",
				logFormat:       "json",
				buildkit:        "never",
			})
			if ece := asExitCode(t, err); ece.Code != 1 {
				t.Errorf("exit code = %d, want 1", ece.Code)
			}
			stdout := out.out.String()
			if !strings.Contains(stdout, `"outcome":"error"`) || !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout = %q, want an error envelope with %q", stdout, tc.want)
			}
		})
	}
}
