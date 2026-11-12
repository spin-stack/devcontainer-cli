package config

import (
	"runtime"
	"testing"
)

func TestOracle_WorkspaceMountBasename(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("oracle pins the linux consistency behavior")
	}
	wc := computeWorkspaceConfig(&Workspace{RootFolderPath: "/home/user/project"}, &DevContainer{}, false, false)
	if wc.WorkspaceFolder != "/workspaces/project" {
		t.Errorf("workspaceFolder = %q, want /workspaces/project", wc.WorkspaceFolder)
	}
	if wc.WorkspaceMount != "type=bind,source=/home/user/project,target=/workspaces/project" {
		t.Errorf("workspaceMount = %q", wc.WorkspaceMount)
	}
}
