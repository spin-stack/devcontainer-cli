package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// buildConfigNotFound runs build with args and returns the path its "config
// not found" error names.
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

// build [path] builds the workspace the argument names, not the current
// directory; --workspace-folder wins over the argument.
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

// A build target names the stage to build, and the Dockerfile is used as it
// is, with no stage name added.
func TestPrepareDockerfileBuildUsesTheConfiguredTarget(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine AS base\nFROM base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.DevContainer{
		ConfigFilePath: filepath.Join(dir, "devcontainer.json"),
		Build:          &config.Build{Dockerfile: "Dockerfile", Target: "base"},
	}
	prep, err := prepareDockerfileBuild(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer prep.Cleanup()
	if prep.StageName != "base" || prep.Path != dockerfile {
		t.Errorf("stage %q at %q, want base at %q", prep.StageName, prep.Path, dockerfile)
	}
}

// Without a target, the final stage gets a name to build, in a Dockerfile
// written next to the user's, whether the Dockerfile is named under build or
// at the top level.
func TestPrepareDockerfileBuildNamesTheFinalStageWithoutATarget(t *testing.T) {
	for name, cfg := range map[string]config.DevContainer{
		"build":     {Build: &config.Build{Dockerfile: "Dockerfile"}},
		"top level": {DockerFile: "Dockerfile"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dockerfile := filepath.Join(dir, "Dockerfile")
			if err := os.WriteFile(dockerfile, []byte("FROM alpine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg.ConfigFilePath = filepath.Join(dir, "devcontainer.json")
			prep, err := prepareDockerfileBuild(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer prep.Cleanup()
			const stage = "dev_container_auto_added_stage_label"
			if prep.StageName != stage || prep.Path != dockerfile+".devcontainer.build" {
				t.Errorf("stage %q at %q, want %s at the written Dockerfile", prep.StageName, prep.Path, stage)
			}
		})
	}
}
