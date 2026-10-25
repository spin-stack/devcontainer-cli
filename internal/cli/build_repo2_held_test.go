package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// The Dockerfile written to name the final stage sits next to the user's with
// a copy of its .dockerignore, and Cleanup removes both and leaves the user's
// files alone.
func TestPrepareDockerfileBuildCleanupRemovesTheWrittenFiles(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile+".dockerignore", []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prep, err := prepareDockerfileBuild(&config.DevContainer{
		ConfigFilePath: filepath.Join(dir, "devcontainer.json"),
		Build:          &config.Build{Dockerfile: "Dockerfile"},
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(prep.Path)
	if err != nil || !strings.Contains(string(written), "AS dev_container_auto_added_stage_label") {
		t.Fatalf("written Dockerfile = %q, %v; want the final stage named", written, err)
	}
	if ignore, err := os.ReadFile(prep.Path + ".dockerignore"); err != nil || string(ignore) != "secret\n" {
		t.Fatalf("written .dockerignore = %q, %v; want the user's copied", ignore, err)
	}
	prep.Cleanup()
	for _, p := range []string{prep.Path, prep.Path + ".dockerignore"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s after Cleanup: %v, want it removed", p, err)
		}
	}
	for _, p := range []string{dockerfile, dockerfile + ".dockerignore"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("user's %s after Cleanup: %v", p, err)
		}
	}
}
