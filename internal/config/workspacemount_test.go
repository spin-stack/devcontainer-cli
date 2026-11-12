package config

import (
	"strings"
	"testing"
)

func TestWorkspaceMount_QuotesCommaPath(t *testing.T) {
	ws := &Workspace{RootFolderPath: "/home/user/project,v2"}
	wc := computeWorkspaceConfig(ws, &DevContainer{}, false, false)
	if !strings.Contains(wc.WorkspaceMount, `"source=/home/user/project,v2"`) {
		t.Errorf("comma source not quoted: %q", wc.WorkspaceMount)
	}
	if !strings.Contains(wc.WorkspaceMount, `"target=/workspaces/project,v2"`) {
		t.Errorf("comma target not quoted: %q", wc.WorkspaceMount)
	}

	wc2 := computeWorkspaceConfig(&Workspace{RootFolderPath: "/home/user/project"}, &DevContainer{}, false, false)
	if strings.Contains(wc2.WorkspaceMount, `"`) {
		t.Errorf("plain path should not be quoted: %q", wc2.WorkspaceMount)
	}
}
