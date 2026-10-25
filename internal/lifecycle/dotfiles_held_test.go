package lifecycle

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// Only GitHub shorthand becomes a github.com URL: TS installDotfiles prefixes a
// repository with no colon that does not match /^\.{0,2}\//, so an absolute or
// relative path and a URL are cloned as written.
func TestInstallDotfilesPrefixesOnlyGitHubShorthand(t *testing.T) {
	for repo, want := range map[string]string{
		"owner/dots":             "https://github.com/owner/dots.git",
		"/srv/dots":              "/srv/dots",
		"./dots":                 "./dots",
		"../dots":                "../dots",
		"git@example.com:o/dots": "git@example.com:o/dots",
	} {
		exec := &mockExecutor{}
		if err := InstallDotfiles(log.Null, exec, DotfilesConfig{Repository: repo, TargetPath: "/t"}); err != nil {
			t.Fatal(err)
		}
		if len(exec.commands) != 1 || !strings.Contains(exec.commands[0], "git clone --depth 1 "+want+" /t ") {
			t.Errorf("%s: commands = %q, want a clone of %s", repo, exec.commands, want)
		}
	}
}
