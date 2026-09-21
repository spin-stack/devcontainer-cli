package cli

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReleaseBuilderConfigsAreGitIgnored guards the release job against a dirty
// tree. useblacksmith/setup-docker-builder writes its BuildKit daemon config with
// a relative path, so it lands in the job's working directory — the repository
// checkout. GoReleaser validates that the tree is clean before releasing, and an
// untracked buildkitd.toml made `goreleaser release` fail with "git is in a dirty
// state". Ignored files do not count as dirty, so the configs must stay ignored.
//
// `git check-ignore --no-index` evaluates the ignore rules for a path without the
// file existing, so the test never touches the working tree.
func TestReleaseBuilderConfigsAreGitIgnored(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	for _, name := range []string{
		"buildkitd.toml",                  // setup_builder.ts: writeTomlConfig("buildkitd.toml", ...)
		"docker-container-buildkitd.toml", // setup_builder.ts: the docker-container fallback builder
	} {
		cmd := exec.Command(gitPath, "check-ignore", "--quiet", "--no-index", name)
		cmd.Dir = repoRoot
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			// Exit 0: the path is ignored.
		case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
			t.Errorf("%s is not git-ignored: the release job's builder writes it into the checkout and GoReleaser then fails on a dirty tree", name)
		default:
			t.Fatalf("git check-ignore %s: %v", name, err)
		}
	}
}
