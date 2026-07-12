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
	// Walk within an isolated tree only: bound the search at `root` so the result
	// does not depend on whether an ancestor of the system temp dir (e.g. /tmp)
	// happens to contain a `.git`.
	root := t.TempDir()
	dir := filepath.Join(root, "x", "y")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectGitRootUntil(dir, root); got != "" {
		t.Errorf("non-repo = %q, want empty", got)
	}
}

// TestDetectGitRootUntilStopsAtCeiling verifies the ceiling itself: a `.git`
// ABOVE stopAt must not be found, while one AT or BELOW stopAt is.
func TestDetectGitRootUntilStopsAtCeiling(t *testing.T) {
	root := t.TempDir()
	// A repo marker above the ceiling.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ceiling := filepath.Join(root, "sub")
	nested := filepath.Join(ceiling, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// root/.git is above `ceiling`, so bounded walk must not find it.
	if got := detectGitRootUntil(nested, ceiling); got != "" {
		t.Errorf("bounded walk found a repo above the ceiling: %q", got)
	}
	// The unbounded walk still finds root/.git.
	if got := detectGitRootUntil(nested, ""); got != root {
		t.Errorf("unbounded walk = %q, want %q", got, root)
	}
}
