package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestComputeWorkspaceConfigOutsideARepository(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	wc := computeWorkspaceConfig(&Workspace{RootFolderPath: ws}, &DevContainer{}, true, true)
	if len(wc.AdditionalMounts) != 0 || wc.WorkspaceFolder != "/workspaces/plain" {
		t.Errorf("additionalMounts = %q, workspaceFolder = %q; want none and /workspaces/plain", wc.AdditionalMounts, wc.WorkspaceFolder)
	}
}

func TestDetectGitRootUntilCleansTheCeiling(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ceiling := filepath.Join(root, "sub")
	nested := filepath.Join(ceiling, "a")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectGitRootUntil(nested, ceiling+string(filepath.Separator)); got != "" {
		t.Errorf("walk with ceiling %q found %q, want no repository", ceiling+string(filepath.Separator), got)
	}
}

func TestLoadResolvesLocalEnvCaseSensitivelyOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the host is Windows")
	}
	t.Setenv("GOOS_OVERRIDE", "")
	t.Setenv("DC_HELD_CASE", "host")
	ws := t.TempDir()
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	writeFile(t, cfg, `{"image":"x","containerEnv":{"EXACT":"${localEnv:DC_HELD_CASE}","LOWER":"${localEnv:dc_held_case}"}}`)
	res, err := LoadDevContainerConfigWithMounts(ws, cfg, "", MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if env := res.Config.ContainerEnv; env["EXACT"] != "host" || env["LOWER"] != "" {
		t.Errorf("containerEnv = %v, want EXACT=host and LOWER empty", env)
	}
}
