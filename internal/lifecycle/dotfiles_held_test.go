package lifecycle

import (
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/log"
)

func TestInstallDotfilesPrefixesOnlyGitHubShorthand(t *testing.T) {
	for repo, want := range map[string]string{
		"owner/dots":             "https://github.com/owner/dots.git",
		"/srv/dots":              "/srv/dots",
		"./dots":                 "./dots",
		"../dots":                "../dots",
		"git@example.com:o/dots": "git@example.com:o/dots",
	} {
		exec := NewMockCommandExecutor(gomock.NewController(t))
		exec.EXPECT().Exec(gomock.Cond(func(command string) bool {
			return strings.Contains(command, "git clone --depth 1 "+want+" /t ")
		}))
		if err := InstallDotfiles(log.Null, exec, DotfilesConfig{Repository: repo, TargetPath: "/t"}); err != nil {
			t.Fatal(err)
		}
	}
}
