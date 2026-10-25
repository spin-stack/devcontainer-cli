package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openWorkspace writes a workspace whose devcontainer.json names an image, and
// returns its folder and config path.
func openWorkspace(t *testing.T) (string, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"alpine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, cfg
}

// wantOpenURI is the folder URI open builds for ws and its config.
func wantOpenURI(t *testing.T, ws, cfg string) string {
	t.Helper()
	uri, err := devcontainerFolderURI(ws, cfg, "/workspaces/"+filepath.Base(ws))
	if err != nil {
		t.Fatal(err)
	}
	return uri
}

func runOpenCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newOpenCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), errOut.String(), err
}

// open [path] --dry-run prints the folder URI of the workspace the argument
// names, and on stderr the editor command that would open it.
func TestOpenDryRunOpensThePositionalWorkspace(t *testing.T) {
	ws, cfg := openWorkspace(t)
	t.Chdir(t.TempDir())
	stdout, stderr, err := runOpenCmd(t, ws, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	uri := wantOpenURI(t, ws, cfg)
	if stdout != uri+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, uri+"\n")
	}
	if want := "code --folder-uri " + uri + "\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// --workspace-folder wins over the positional path.
func TestOpenWorkspaceFlagWinsOverThePositionalPath(t *testing.T) {
	ws, cfg := openWorkspace(t)
	stdout, _, err := runOpenCmd(t, t.TempDir(), "--workspace-folder", ws, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if uri := wantOpenURI(t, ws, cfg); stdout != uri+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, uri+"\n")
	}
}

// Without a path open opens the current directory, through the editor
// --editor names.
func TestOpenDefaultsToTheCurrentDirectory(t *testing.T) {
	ws, cfg := openWorkspace(t)
	t.Chdir(ws)
	stdout, stderr, err := runOpenCmd(t, "--dry-run", "--editor", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	uri := wantOpenURI(t, ws, cfg)
	if stdout != uri+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, uri+"\n")
	}
	if want := "cursor --folder-uri " + uri + "\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// An empty editor falls back to code.
func TestOpenEmptyEditorIsCode(t *testing.T) {
	ws, cfg := openWorkspace(t)
	var out captureOutput
	if err := runOpen(t.Context(), &out, openOpts{workspaceFolder: ws, dryRun: true}); err != nil {
		t.Fatal(err)
	}
	if want := "code --folder-uri " + wantOpenURI(t, ws, cfg) + "\n"; out.err.String() != want {
		t.Errorf("stderr = %q, want %q", out.err.String(), want)
	}
}

// --config names the devcontainer.json the URI carries.
func TestOpenConfigFlagNamesTheConfigFile(t *testing.T) {
	ws, _ := openWorkspace(t)
	other := filepath.Join(ws, "other.json")
	if err := os.WriteFile(other, []byte(`{"image":"debian"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runOpenCmd(t, ws, "--config", other, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if uri := wantOpenURI(t, ws, other); stdout != uri+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, uri+"\n")
	}
}

// A workspace without a devcontainer.json fails before any editor runs.
func TestOpenWithoutAConfigFails(t *testing.T) {
	stdout, _, err := runOpenCmd(t, t.TempDir(), "--dry-run")
	if err == nil {
		t.Fatal("open succeeded without a devcontainer.json")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// writeEditor writes an editor launcher that prints its arguments on stdout, a
// line on stderr, and exits with code.
func writeEditor(t *testing.T, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "editor")
	script := "#!/bin/sh\necho \"$*\"\necho launched >&2\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Without --dry-run open runs the editor with the folder URI, passing its
// output through.
func TestOpenRunsTheEditor(t *testing.T) {
	ws, cfg := openWorkspace(t)
	editor := writeEditor(t, 0)
	stdout, stderr, err := runOpenCmd(t, ws, "--editor", editor)
	if err != nil {
		t.Fatal(err)
	}
	if want := "--folder-uri " + wantOpenURI(t, ws, cfg) + "\n"; stdout != want {
		t.Errorf("stdout = %q, want the editor's %q", stdout, want)
	}
	if want := "Opening " + ws + " in VS Code (dev container)...\nlaunched\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// An editor that fails fails open.
func TestOpenFailsWhenTheEditorFails(t *testing.T) {
	ws, _ := openWorkspace(t)
	editor := writeEditor(t, 3)
	_, _, err := runOpenCmd(t, ws, "--editor", editor)
	if err == nil || !strings.Contains(err.Error(), "launch \""+editor+"\"") {
		t.Fatalf("err = %v, want the launch failure", err)
	}
}
