package lifecycle

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

// DotfilesConfig holds dotfiles installation settings.
type DotfilesConfig struct {
	Repository     string
	InstallCommand string
	TargetPath     string // default: ~/dotfiles
}

// localRepository is TS installDotfiles' test for a repository that is a path,
// absolute or relative, and so is not GitHub shorthand.
var localRepository = regexp.MustCompile(`^\.{0,2}/`)

// InstallDotfiles clones and runs a dotfiles repository in the container.
func InstallDotfiles(logger log.Logger, executor CommandExecutor, config DotfilesConfig) error {
	if config.Repository == "" {
		return nil
	}

	repo := config.Repository
	// Auto-prefix GitHub shorthand: a repository with no colon that is not a
	// path (TS: `repository.indexOf(':') === -1 && !/^\.{0,2}\//.test(repository)`).
	if !strings.Contains(repo, ":") && !localRepository.MatchString(repo) {
		repo = "https://github.com/" + repo + ".git"
	}

	targetPath := config.TargetPath
	if targetPath == "" {
		targetPath = "~/dotfiles"
	}

	logger.Event(log.Event{
		Type:   "progress",
		Name:   "Installing Dotfiles",
		Status: "running",
	})

	// Idempotency marker: install once per container, so editor reconnections
	// (repeated `up`s) don't re-clone/re-run the dotfiles — matching the TS CLI.
	markerPrefix := `MARKER="$HOME/.devcontainer/.dotfilesMarker"
if [ -e "$MARKER" ]; then echo "dotfiles already installed"; exit 0; fi
mkdir -p "$(dirname "$MARKER")" && : > "$MARKER"
`

	// Expand a leading `~/` to `$HOME/`: the command is embedded in double quotes,
	// where `$HOME` expands but a bare `~` does not, so `~/install.sh` would be run
	// as a literal directory named `~` and fail.
	installCmd := config.InstallCommand
	if strings.HasPrefix(installCmd, "~/") {
		installCmd = "$HOME/" + installCmd[2:]
	}

	var script string
	if config.InstallCommand != "" {
		script = fmt.Sprintf(`command -v git >/dev/null 2>&1 || exit 1
[ -e %s ] || git clone --depth 1 %s %s || exit $?
cd %s
if [ -f "./%s" ]; then
  [ -x "./%s" ] || chmod +x "./%s"
  "./%s"
elif [ -f "%s" ]; then
  [ -x "%s" ] || chmod +x "%s"
  "%s"
else
  echo "Could not locate '%s'"
  exit 126
fi`,
			targetPath, repo, targetPath,
			targetPath,
			installCmd,
			installCmd, installCmd,
			installCmd,
			installCmd,
			installCmd, installCmd,
			installCmd,
			installCmd,
		)
	} else {
		script = fmt.Sprintf(`command -v git >/dev/null 2>&1 || exit 1
[ -e %s ] || git clone --depth 1 %s %s || exit $?
cd %s
for f in install.sh install bootstrap.sh bootstrap script/bootstrap setup.sh setup script/setup; do
  if [ -e "$f" ]; then
    installCommand=$f
    break
  fi
done
if [ -z "$installCommand" ]; then
  dotfiles=$(ls -d %s/.* 2>/dev/null | grep -v -E '/(\.|\.\.|\.git)$')
  if [ ! -z "$dotfiles" ]; then
    ln -sf $dotfiles ~ 2>/dev/null
  fi
else
  [ -x "$installCommand" ] || chmod +x "$installCommand"
  ./"$installCommand"
fi`,
			targetPath, repo, targetPath,
			targetPath,
			targetPath,
		)
	}

	err := executor.Exec(markerPrefix + script)
	if err != nil {
		logger.Event(log.Event{
			Type:   "progress",
			Name:   "Installing Dotfiles",
			Status: "failed",
		})
		return err
	}

	logger.Event(log.Event{
		Type:   "progress",
		Name:   "Installing Dotfiles",
		Status: "succeeded",
	})
	return nil
}
