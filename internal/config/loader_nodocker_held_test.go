package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A --config that does not exist fails with the read error itself, which the
// CLI reports as the missing config (TS: "Dev container config (...) not found.").
func TestLoadReportsAMissingConfigFile(t *testing.T) {
	ws := t.TempDir()
	missing := filepath.Join(ws, "nope.json")
	_, err := LoadDevContainerConfigWithMounts(ws, missing, "", MountOptions{})
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "read config "+missing) {
		t.Fatalf("err = %v, want the read error for %s", err, missing)
	}
}

// The config file path is the --config file, else the one found in the
// workspace or, with only an override, the workspace's default path, else
// without a workspace the override file (TS resolveWithLocalFolder's
// configPath; readDevContainerConfigFile reads overrideConfigFile ??
// configFile but keeps configFile) - never a "configFilePath" property of the
// file - and it is what host substitution errors name.
func TestLoadSetsTheConfigFilePath(t *testing.T) {
	ws := t.TempDir()
	cfg := filepath.Join(ws, "base.json")
	writeFile(t, cfg, `{"image":"x","configFilePath":"/bogus.json"}`)
	res, err := LoadDevContainerConfigWithMounts(ws, cfg, "", MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.ConfigFilePath != cfg || res.HostSub.ConfigFilePath != cfg {
		t.Errorf("ConfigFilePath = %q, HostSub.ConfigFilePath = %q, want %q", res.Config.ConfigFilePath, res.HostSub.ConfigFilePath, cfg)
	}

	override := filepath.Join(ws, "override.json")
	writeFile(t, override, `{"image":"y"}`)
	defaultPath := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	res, err = LoadDevContainerConfigWithMounts(ws, "", override, MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.ConfigFilePath != defaultPath || res.HostSub.ConfigFilePath != defaultPath {
		t.Errorf("override only: ConfigFilePath = %q, HostSub.ConfigFilePath = %q, want %q", res.Config.ConfigFilePath, res.HostSub.ConfigFilePath, defaultPath)
	}
	res, err = LoadDevContainerConfigWithMounts("", "", override, MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.ConfigFilePath != override || res.HostSub.ConfigFilePath != override {
		t.Errorf("override without a workspace: ConfigFilePath = %q, HostSub.ConfigFilePath = %q, want %q", res.Config.ConfigFilePath, res.HostSub.ConfigFilePath, override)
	}

	writeFile(t, override, `{"image":"y","containerEnv":{"A":"${localEnv}"}}`)
	_, err = LoadDevContainerConfigWithMounts(ws, "", override, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "in devcontainer.json") {
		t.Errorf("substitution error = %v, want it to name the default devcontainer.json", err)
	}
	_, err = LoadDevContainerConfigWithMounts("", "", override, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "in override.json") {
		t.Errorf("substitution error without a workspace = %v, want it to name override.json", err)
	}
}

// workspaceFolder and workspaceMount are reported with their host variables
// resolved (TS getWorkspaceConfiguration reads the substituted config).
func TestLoadResolvesWorkspaceFolderAndMount(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "proj")
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	writeFile(t, cfg, `{
		"image": "x",
		"workspaceFolder": "/w/${localWorkspaceFolderBasename}",
		"workspaceMount": "source=${localWorkspaceFolder},target=/w/${localWorkspaceFolderBasename},type=bind"
	}`)
	res, err := LoadDevContainerConfigWithMounts(ws, cfg, "", MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.WorkspaceConfig.WorkspaceFolder; got != "/w/proj" {
		t.Errorf("WorkspaceFolder = %q, want /w/proj", got)
	}
	if got, want := res.WorkspaceConfig.WorkspaceMount, "source="+ws+",target=/w/proj,type=bind"; got != want {
		t.Errorf("WorkspaceMount = %q, want %q", got, want)
	}
}

// With --mount-workspace-git-root a workspace below its Git root mounts the
// root and works in the subfolder (TS getWorkspaceConfiguration); without it
// the workspace mounts by itself.
func TestComputeWorkspaceConfigWorksInTheGitRootsSubfolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := computeWorkspaceConfig(&Workspace{RootFolderPath: sub}, &DevContainer{}, true, false).WorkspaceFolder; got != "/workspaces/repo/a/b" {
		t.Errorf("git root mounted: WorkspaceFolder = %q, want /workspaces/repo/a/b", got)
	}
	if got := computeWorkspaceConfig(&Workspace{RootFolderPath: root}, &DevContainer{}, true, false).WorkspaceFolder; got != "/workspaces/repo" {
		t.Errorf("at the git root: WorkspaceFolder = %q, want /workspaces/repo", got)
	}
	if got := computeWorkspaceConfig(&Workspace{RootFolderPath: sub}, &DevContainer{}, false, false).WorkspaceFolder; got != "/workspaces/b" {
		t.Errorf("git root not mounted: WorkspaceFolder = %q, want /workspaces/b", got)
	}
}

// An empty COMPOSE_FILE is no COMPOSE_FILE, and a .env's COMPOSE_FILE comes
// next (TS getDockerComposeFilePaths: `if (envComposeFile)`, then the .env file).
func TestDockerComposeFilePathsFromTheDotEnv(t *testing.T) {
	dir := t.TempDir()
	c := &DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json")}
	got, err := DockerComposeFilePaths(c, map[string]string{"COMPOSE_FILE": ""}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "docker-compose.yml")}; !slices.Equal(got, want) {
		t.Errorf("empty COMPOSE_FILE: paths = %v, want %v", got, want)
	}

	writeFile(t, filepath.Join(dir, ".env"), "OTHER=1\nCOMPOSE_FILE=a.yml"+string(os.PathListSeparator)+"b.yml\n")
	got, err = DockerComposeFilePaths(c, map[string]string{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "a.yml"), filepath.Join(dir, "b.yml")}; !slices.Equal(got, want) {
		t.Errorf(".env COMPOSE_FILE: paths = %v, want %v", got, want)
	}
}
