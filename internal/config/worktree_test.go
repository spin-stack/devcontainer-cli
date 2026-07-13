package config

// Oracle tests ported from the upstream devcontainers CLI:
//   reference/src/test/workspaceConfiguration.test.ts
//   (describe 'getWorkspaceConfiguration' > 'git worktree handling').
//
// The container-side targets (/workspaces/...) are independent of the temp
// prefix — they derive from the path segments below the common ancestor of the
// worktree and its common dir — so the fixtures use a real t.TempDir() and the
// host-side source paths are computed relative to it. Pinned to linux, where the
// bind mount carries no consistency= suffix (computeWorkspaceConfig reads
// runtime.GOOS directly and cannot be faked hermetically).

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeGitlink creates a worktree at <base>/<worktreeRel> whose `.git` gitlink
// file holds the given gitdir line, and returns the absolute worktree path.
func writeGitlink(t *testing.T, base, worktreeRel, gitdirLine string) string {
	t.Helper()
	wt := filepath.Join(base, filepath.FromSlash(worktreeRel))
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(gitdirLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestWorktreeCommonDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("oracle pins the linux consistency behavior (empty suffix)")
	}

	t.Run("relative gitdir adds a common-dir mount", func(t *testing.T) {
		base := t.TempDir()
		wt := writeGitlink(t, base, "worktrees/feature", "gitdir: ../../repo/.git/worktrees/feature")
		commonDir := filepath.Join(base, "repo", ".git")

		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: wt}, &DevContainer{}, true, true)

		if wc.WorkspaceFolder != "/workspaces/worktrees/feature" {
			t.Errorf("workspaceFolder = %q", wc.WorkspaceFolder)
		}
		if want := "type=bind,source=" + wt + ",target=/workspaces/worktrees/feature"; wc.WorkspaceMount != want {
			t.Errorf("workspaceMount = %q, want %q", wc.WorkspaceMount, want)
		}
		want := []string{"type=bind,source=" + commonDir + ",target=/workspaces/repo/.git"}
		if len(wc.AdditionalMounts) != 1 || wc.AdditionalMounts[0] != want[0] {
			t.Errorf("additionalMounts = %q, want %q", wc.AdditionalMounts, want)
		}
	})

	t.Run("single level up", func(t *testing.T) {
		base := t.TempDir()
		wt := writeGitlink(t, base, "repo-worktree", "gitdir: ../repo/.git/worktrees/worktree")
		commonDir := filepath.Join(base, "repo", ".git")

		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: wt}, &DevContainer{}, true, true)

		if wc.WorkspaceFolder != "/workspaces/repo-worktree" {
			t.Errorf("workspaceFolder = %q", wc.WorkspaceFolder)
		}
		if want := "type=bind,source=" + commonDir + ",target=/workspaces/repo/.git"; len(wc.AdditionalMounts) != 1 || wc.AdditionalMounts[0] != want {
			t.Errorf("additionalMounts = %q, want [%q]", wc.AdditionalMounts, want)
		}
	})

	t.Run("two levels deep from common parent", func(t *testing.T) {
		base := t.TempDir()
		wt := writeGitlink(t, base, "projects/worktrees/feature", "gitdir: ../../repos/main/.git/worktrees/feature")
		commonDir := filepath.Join(base, "projects", "repos", "main", ".git")

		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: wt}, &DevContainer{}, true, true)

		if wc.WorkspaceFolder != "/workspaces/worktrees/feature" {
			t.Errorf("workspaceFolder = %q", wc.WorkspaceFolder)
		}
		if want := "type=bind,source=" + wt + ",target=/workspaces/worktrees/feature"; wc.WorkspaceMount != want {
			t.Errorf("workspaceMount = %q, want %q", wc.WorkspaceMount, want)
		}
		if want := "type=bind,source=" + commonDir + ",target=/workspaces/repos/main/.git"; len(wc.AdditionalMounts) != 1 || wc.AdditionalMounts[0] != want {
			t.Errorf("additionalMounts = %q, want [%q]", wc.AdditionalMounts, want)
		}
	})

	t.Run("two levels deep with workspace in subfolder", func(t *testing.T) {
		base := t.TempDir()
		writeGitlink(t, base, "projects/worktrees/feature", "gitdir: ../../repos/main/.git/worktrees/feature")
		sub := filepath.Join(base, "projects", "worktrees", "feature", "packages", "app")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(base, "projects", "worktrees", "feature")
		commonDir := filepath.Join(base, "projects", "repos", "main", ".git")

		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: sub}, &DevContainer{}, true, true)

		if wc.WorkspaceFolder != "/workspaces/worktrees/feature/packages/app" {
			t.Errorf("workspaceFolder = %q", wc.WorkspaceFolder)
		}
		if want := "type=bind,source=" + wt + ",target=/workspaces/worktrees/feature"; wc.WorkspaceMount != want {
			t.Errorf("workspaceMount = %q, want %q", wc.WorkspaceMount, want)
		}
		if want := "type=bind,source=" + commonDir + ",target=/workspaces/repos/main/.git"; len(wc.AdditionalMounts) != 1 || wc.AdditionalMounts[0] != want {
			t.Errorf("additionalMounts = %q, want [%q]", wc.AdditionalMounts, want)
		}
	})
}

// TestWorktreeCommonDirNoMount covers the cases that must NOT produce an extra
// mount: a normal clone (.git dir), an absolute gitdir, and the flag disabled.
func TestWorktreeCommonDirNoMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("oracle pins the linux consistency behavior (empty suffix)")
	}

	t.Run("normal clone (.git is a directory)", func(t *testing.T) {
		base := t.TempDir()
		repo := filepath.Join(base, "project")
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: repo}, &DevContainer{}, true, true)
		if len(wc.AdditionalMounts) != 0 {
			t.Errorf("additionalMounts = %q, want none for a normal clone", wc.AdditionalMounts)
		}
		if wc.WorkspaceFolder != "/workspaces/project" {
			t.Errorf("workspaceFolder = %q", wc.WorkspaceFolder)
		}
	})

	t.Run("absolute gitdir", func(t *testing.T) {
		base := t.TempDir()
		wt := writeGitlink(t, base, "project", "gitdir: /absolute/path/to/.git/worktrees/project")
		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: wt}, &DevContainer{}, true, true)
		if len(wc.AdditionalMounts) != 0 {
			t.Errorf("additionalMounts = %q, want none for an absolute gitdir", wc.AdditionalMounts)
		}
	})

	t.Run("flag disabled", func(t *testing.T) {
		base := t.TempDir()
		wt := writeGitlink(t, base, "worktrees/feature", "gitdir: ../../repo/.git/worktrees/feature")
		wc := computeWorkspaceConfig(&Workspace{RootFolderPath: wt}, &DevContainer{}, true, false)
		if len(wc.AdditionalMounts) != 0 {
			t.Errorf("additionalMounts = %q, want none when the flag is off", wc.AdditionalMounts)
		}
		// Without the remap the mount is the plain basename.
		if want := "type=bind,source=" + wt + ",target=/workspaces/feature"; wc.WorkspaceMount != want {
			t.Errorf("workspaceMount = %q, want %q", wc.WorkspaceMount, want)
		}
	})
}

// TestLoadDevContainerConfigWithMountsWorktree locks the full config-load wiring:
// the MountGitWorktreeCommonDir option reaches computeWorkspaceConfig and the
// resulting LoadResult carries the common-dir mount.
func TestLoadDevContainerConfigWithMountsWorktree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("oracle pins the linux consistency behavior (empty suffix)")
	}
	base := t.TempDir()
	wt := writeGitlink(t, base, "worktrees/feature", "gitdir: ../../repo/.git/worktrees/feature")
	if err := os.MkdirAll(filepath.Join(wt, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".devcontainer", "devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	commonDir := filepath.Join(base, "repo", ".git")

	res, err := LoadDevContainerConfigWithMounts(wt, "", "", MountOptions{MountWorkspaceGitRoot: true, MountGitWorktreeCommonDir: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := "type=bind,source=" + commonDir + ",target=/workspaces/repo/.git"
	if got := res.WorkspaceConfig.AdditionalMounts; len(got) != 1 || got[0] != want {
		t.Errorf("AdditionalMounts = %q, want [%q]", got, want)
	}
	if res.WorkspaceConfig.WorkspaceFolder != "/workspaces/worktrees/feature" {
		t.Errorf("WorkspaceFolder = %q", res.WorkspaceConfig.WorkspaceFolder)
	}

	// Default load (no worktree option) must NOT add the mount.
	res2, err := LoadDevContainerConfig(wt, "", "")
	if err != nil {
		t.Fatalf("default load: %v", err)
	}
	if len(res2.WorkspaceConfig.AdditionalMounts) != 0 {
		t.Errorf("default load AdditionalMounts = %q, want none", res2.WorkspaceConfig.AdditionalMounts)
	}
}
