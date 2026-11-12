package devtools

import (
	"maps"
	"slices"
	"strings"
)

func ParityAffected(files []string) string {
	full := false
	cmds := map[string]bool{}

	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		switch kind, cmd := classifyParityFile(f); kind {
		case fileFull:
			full = true
		case fileCommand:
			for _, c := range cmd {
				cmds[c] = true
			}
		case fileIgnore:
		}
	}

	if full {
		return "all"
	}
	if len(cmds) == 0 {
		return "none"
	}
	return strings.Join(slices.Sorted(maps.Keys(cmds)), ",")
}

type fileKind int

const (
	fileIgnore fileKind = iota
	fileFull
	fileCommand
)

func classifyParityFile(f string) (fileKind, []string) {
	switch {
	case strings.HasSuffix(f, ".md"),
		strings.HasPrefix(f, "LICENSE"),
		f == ".gitignore", f == ".editorconfig", f == ".github/CODEOWNERS",
		f == ".github/workflows/release.yml", strings.HasPrefix(f, ".goreleaser"):
		return fileIgnore, nil

	case f == "docs/parity/parity-matrix.yaml",
		f == "internal/cli/parity_matrix_test.go",
		f == "internal/cli/parity_matrix_helpers_test.go",
		f == "Taskfile.yml", f == "go.mod", f == "go.sum",
		f == ".github/workflows/go-cli.yml":
		return fileFull, nil

	case f == "internal/cli/read_configuration.go":
		return fileCommand, []string{"read-configuration"}
	case f == "internal/cli/exec.go":
		return fileCommand, []string{"exec"}
	case f == "internal/cli/outdated.go":
		return fileCommand, []string{"outdated"}
	case f == "internal/cli/features_info.go":
		return fileCommand, []string{"features-info"}
	case f == "internal/cli/templates_apply.go", f == "internal/cli/templates_metadata.go":
		return fileCommand, []string{"templates"}
	case f == "internal/cli/run_user_commands.go":
		return fileCommand, []string{"run-user-commands"}
	case f == "internal/cli/setup.go":
		return fileCommand, []string{"set-up"}
	case f == "internal/cli/gpu.go", f == "internal/cli/mounts.go", f == "internal/cli/up.go":
		return fileCommand, []string{"up"}
	case f == "internal/cli/build.go", f == "internal/cli/build_auth.go", f == "internal/cli/cache_key.go":
		return fileCommand, []string{"build"}
	case f == "internal/cli/collection_commands.go":
		return fileCommand, []string{"features", "templates", "features-info", "features-package"}

	case strings.HasPrefix(f, "internal/cli/") && strings.HasSuffix(f, "_test.go"):
		return fileIgnore, nil

	case strings.HasPrefix(f, "internal/"), strings.HasPrefix(f, "cmd/"):
		return fileFull, nil

	default:
		return fileFull, nil
	}
}
