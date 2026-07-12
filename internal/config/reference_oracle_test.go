package config

// Oracle test ported from the upstream devcontainers CLI:
//   reference/src/test/workspaceConfiguration.test.ts
//   (describe 'getWorkspaceConfiguration' > 'basic workspace mounting').
//
// Only the basic /workspaces/<basename> mount is ported: the rest of that suite
// exercises git-worktree common-dir mounting, which is a documented divergence
// (see docs/DIVERGENCES.md) — we deliberately do not implement it, so porting
// those would import behavior we do not have. The platform matrix is reduced to
// linux, the CI runtime (consistency is empty on linux; computeWorkspaceConfig
// reads runtime.GOOS directly and cannot be faked hermetically).

import (
	"runtime"
	"testing"
)

func TestOracle_WorkspaceMountBasename(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("oracle pins the linux consistency behavior")
	}
	// mountWorkspaceGitRoot=false, no .git → the workspace folder mounts directly.
	wc := computeWorkspaceConfig(&Workspace{RootFolderPath: "/home/user/project"}, &DevContainer{}, false)
	if wc.WorkspaceFolder != "/workspaces/project" {
		t.Errorf("workspaceFolder = %q, want /workspaces/project", wc.WorkspaceFolder)
	}
	if wc.WorkspaceMount != "type=bind,source=/home/user/project,target=/workspaces/project" {
		t.Errorf("workspaceMount = %q", wc.WorkspaceMount)
	}
}
