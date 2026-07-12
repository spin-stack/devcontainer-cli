package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGitRoot(t *testing.T) {
	root := t.TempDir()
	// root/.git (dir) + nested sub/dir
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// From a nested dir, the root is found by walking up.
	if got := detectGitRoot(nested); got != root {
		t.Errorf("from nested = %q, want %q", got, root)
	}
	// From the root itself.
	if got := detectGitRoot(root); got != root {
		t.Errorf("from root = %q, want %q", got, root)
	}

	// A `.git` gitlink FILE (worktree/submodule) is also accepted as a root.
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectGitRoot(wt); got != wt {
		t.Errorf("gitlink file = %q, want %q", got, wt)
	}
}

func TestDetectGitRootNotARepo(t *testing.T) {
	// A directory with no .git anywhere up to the temp root returns "".
	dir := filepath.Join(t.TempDir(), "x", "y")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectGitRoot(dir); got != "" {
		t.Errorf("non-repo = %q, want empty", got)
	}
}
