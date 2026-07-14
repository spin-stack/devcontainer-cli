// Package devtools holds the pure logic behind the repo's CI helper commands
// (invoked via cmd/devtool). Keeping the decision logic here — rather than in
// shell scripts — makes it unit-testable; the thin main only wires stdin/stdout,
// git, and `go tool` around these functions.
package devtools

import (
	"sort"
	"strings"
)

// ParityAffected maps a set of changed files to the parity runtime commands they
// can affect, returning one of:
//
//	"all"   — run the whole runtime matrix (a shared/core or uncertain file changed)
//	"none"  — run nothing (only docs/irrelevant files, or no files)
//	"<csv>" — run only these commands, e.g. "build,up" (a PARITY_COMMAND allowlist)
//
// The mapping is deliberately conservative: only files we are confident are
// command-local narrow the run; everything else falls through to "all". The daily
// full run is the backstop for any cross-command effect this misses. Ported from
// the former scripts/parity-affected.sh; the ordering of checks is significant
// (first match wins, exactly like the shell `case`).
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
			// contributes nothing
		}
	}

	if full {
		return "all"
	}
	if len(cmds) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(cmds))
	for k := range cmds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

type fileKind int

const (
	fileIgnore fileKind = iota
	fileFull
	fileCommand
)

// classifyParityFile mirrors the ordered shell `case` in the original script:
// the first matching arm wins. Returns the parity commands only for fileCommand.
func classifyParityFile(f string) (fileKind, []string) {
	switch {
	// --- Ignorable: never affect the runtime matrix ------------------------
	case strings.HasSuffix(f, ".md"),
		strings.HasPrefix(f, "LICENSE"),
		f == ".gitignore", f == ".editorconfig", f == ".github/CODEOWNERS",
		f == ".github/workflows/release.yml", strings.HasPrefix(f, ".goreleaser"):
		return fileIgnore, nil

	// --- Harness / matrix data / build config → run everything -------------
	// (listed BEFORE the generic *_test.go ignore so it wins)
	case f == "docs/parity/parity-matrix.yaml",
		f == "internal/cli/parity_matrix_test.go",
		f == "internal/cli/parity_matrix_helpers_test.go",
		f == "Taskfile.yml", f == "go.mod", f == "go.sum",
		f == ".github/workflows/go-cli.yml":
		return fileFull, nil

	// --- Isolated leaf command files → just that command's cases -----------
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

	// --- A command's own hermetic/e2e tests don't affect the runtime matrix -
	case strings.HasPrefix(f, "internal/cli/") && strings.HasSuffix(f, "_test.go"):
		return fileIgnore, nil

	// --- Anything else under the source tree is shared or uncertain → full -
	case strings.HasPrefix(f, "internal/"), strings.HasPrefix(f, "cmd/"):
		return fileFull, nil

	// --- Unknown top-level path → be safe ----------------------------------
	default:
		return fileFull, nil
	}
}
