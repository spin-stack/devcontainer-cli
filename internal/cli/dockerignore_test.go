package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// upstream #969: a renamed build Dockerfile must keep the sibling
// <Dockerfile>.dockerignore so BuildKit still applies it.
func TestCopyDockerignore(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "Dockerfile")
	tmp := orig + ".devcontainer.build"
	if err := os.WriteFile(orig+".dockerignore", []byte("node_modules\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyDockerignore(orig, tmp)
	got, err := os.ReadFile(tmp + ".dockerignore")
	if err != nil {
		t.Fatalf("renamed .dockerignore not created: %v", err)
	}
	if string(got) != "node_modules\n*.log\n" {
		t.Errorf("content = %q", got)
	}
	// No sibling ignore file → no-op, no error.
	copyDockerignore(filepath.Join(dir, "Other"), filepath.Join(dir, "Other.tmp"))
	if _, err := os.Stat(filepath.Join(dir, "Other.tmp.dockerignore")); !os.IsNotExist(err) {
		t.Error("should not create an ignore file when none exists")
	}
}
