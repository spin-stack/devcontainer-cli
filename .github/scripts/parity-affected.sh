#!/usr/bin/env bash
# Map a set of changed files (read from stdin, one path per line) to the parity
# runtime commands they can affect, and print a single value on stdout:
#
#   all   -> run the whole runtime matrix (a shared/core or uncertain file changed)
#   none  -> run nothing (only docs / irrelevant files changed, or no files)
#   <csv> -> run only these commands, e.g. "up,build" (PARITY_COMMAND allowlist)
#
# The mapping is deliberately conservative: only files we are confident are
# command-local narrow the run; everything else falls through to "all". The
# daily full run is the backstop for any cross-command effect this misses.
#
# Usage:  git diff --name-only base...head | .github/scripts/parity-affected.sh
set -euo pipefail

full=0
any=0
declare -A cmds=()

add() { cmds["$1"]=1; }

while IFS= read -r f; do
  [ -z "$f" ] && continue
  any=1
  case "$f" in
    # --- Ignorable: never affect the runtime matrix -------------------------
    *.md | LICENSE* | .gitignore | .editorconfig | .github/CODEOWNERS)
      : ;;
    .github/workflows/release.yml | .goreleaser* )
      : ;;

    # --- Harness / matrix data / build config → run everything --------------
    # (listed BEFORE the generic *_test.go ignore so it wins)
    docs/parity/parity-matrix.yaml | \
    internal/cli/parity_matrix_test.go | internal/cli/parity_matrix_helpers_test.go | \
    Taskfile.yml | go.mod | go.sum | \
    .github/workflows/go-cli.yml | .github/scripts/parity-affected.sh)
      full=1 ;;

    # --- Isolated leaf command files → just that command's cases ------------
    internal/cli/read_configuration.go)                            add read-configuration ;;
    internal/cli/exec.go)                                          add exec ;;
    internal/cli/outdated.go)                                      add outdated ;;
    internal/cli/features_info.go)                                 add features-info ;;
    internal/cli/templates_apply.go | internal/cli/templates_metadata.go) add templates ;;
    internal/cli/run_user_commands.go)                             add run-user-commands ;;
    internal/cli/setup.go)                                         add set-up ;;
    internal/cli/gpu.go | internal/cli/mounts.go | internal/cli/up.go) add up ;;
    internal/cli/build.go | internal/cli/build_auth.go | internal/cli/cache_key.go) add build ;;
    internal/cli/collection_commands.go)
      add features; add templates; add features-info; add features-package ;;

    # --- A command's own hermetic/e2e tests don't affect the runtime matrix -
    # (they run in their own jobs). Ignore them for runtime selection.
    internal/cli/*_test.go)
      : ;;

    # --- Anything else under the source tree is shared or uncertain → full --
    internal/* | cmd/*)
      full=1 ;;

    # --- Unknown top-level path → be safe --------------------------------------
    *)
      full=1 ;;
  esac
done

if [ "$full" -eq 1 ]; then
  echo "all"
  exit 0
fi
if [ "${#cmds[@]}" -eq 0 ]; then
  # any==0 means an empty diff; either way there is nothing runtime-relevant.
  echo "none"
  exit 0
fi

# Join the selected command keys with commas, sorted for stable output.
printf '%s\n' "${!cmds[@]}" | sort | paste -sd, -
