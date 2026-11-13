package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func buildConfigNotFound(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newBuildCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	_ = cmd.ExecuteContext(t.Context())
	var got struct{ Outcome, Message string }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON result: %v\n%s", err, out.String())
	}
	if got.Outcome != "error" {
		t.Fatalf("outcome = %q, want error", got.Outcome)
	}
	return got.Message
}

func TestBuildPositionalPathIsTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	t.Chdir(t.TempDir())
	want := "Dev container config (" + filepath.Join(ws, ".devcontainer", "devcontainer.json") + ") not found."
	if got := buildConfigNotFound(t, ws); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got := buildConfigNotFound(t, t.TempDir(), "--workspace-folder", ws); got != want {
		t.Errorf("message with --workspace-folder = %q, want %q", got, want)
	}
}
