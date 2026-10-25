package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The oracle's node is the one on this process's PATH, named by its absolute
// path, so a login shell's PATH cannot swap it for another node.
func TestDefaultParityTSCLIUsesThisProcessNode(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := defaultParityTSCLI("/repo")
	if want := node + " " + filepath.Join("/repo", "reference", "devcontainer.js"); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}

	t.Setenv("PATH", t.TempDir())
	if got := defaultParityTSCLI("/repo"); !strings.HasPrefix(got, "node ") {
		t.Errorf("without node on PATH the command = %q, want a bare node for the shell to find", got)
	}
}
