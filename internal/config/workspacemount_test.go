package config

import (
	"strings"
	"testing"
)

// TestWorkspaceMount_QuotesCommaPath covers the fix: a workspace path containing
// a comma must be quoted in the bind spec, or Docker parses the comma as a mount-
// option boundary and the mount breaks (matches the TS srcQuote/tgtQuote guard).
func TestWorkspaceMount_QuotesCommaPath(t *testing.T) {
	ws := &Workspace{RootFolderPath: "/home/user/project,v2"}
	wc := computeWorkspaceConfig(ws, &DevContainer{}, false)
	if !strings.Contains(wc.WorkspaceMount, `"source=/home/user/project,v2"`) {
		t.Errorf("comma source not quoted: %q", wc.WorkspaceMount)
	}
	if !strings.Contains(wc.WorkspaceMount, `"target=/workspaces/project,v2"`) {
		t.Errorf("comma target not quoted: %q", wc.WorkspaceMount)
	}

	// A normal path is NOT quoted.
	wc2 := computeWorkspaceConfig(&Workspace{RootFolderPath: "/home/user/project"}, &DevContainer{}, false)
	if strings.Contains(wc2.WorkspaceMount, `"`) {
		t.Errorf("plain path should not be quoted: %q", wc2.WorkspaceMount)
	}
}
