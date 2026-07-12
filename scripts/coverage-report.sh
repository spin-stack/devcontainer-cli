#!/usr/bin/env bash
# Render a per-package coverage table (plus the module total) as GitHub-flavored
# Markdown — readable in a terminal and appendable to $GITHUB_STEP_SUMMARY.
#
# Usage: coverage-report.sh <profile.out | covdata-dir> [title]
#
# Accepts either a text cover profile or a covdata directory (GOCOVERDIR /
# `go test -test.gocoverdir` output); a directory is converted with covdata textfmt.
set -euo pipefail

src="${1:?usage: coverage-report.sh <profile|covdata-dir> [title]}"
title="${2:-coverage}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mod="github.com/devcontainers/cli/"

profile="$src"
if [ -d "$src" ]; then
  if [ -z "$(ls -A "$src" 2>/dev/null)" ]; then
    printf '### Coverage — %s\n\n_(no coverage data)_\n' "$title"
    exit 0
  fi
  profile="${src%/}.out"
  go tool covdata textfmt -i="$src" -o="$profile"
fi
if [ ! -s "$profile" ]; then
  printf '### Coverage — %s\n\n_(no coverage data)_\n' "$title"
  exit 0
fi

rows="$(awk -f "$here/pkgcov.awk" "$profile")"
total="$(printf '%s\n' "$rows" | awk -F'\t' '$1=="TOTAL"{printf "%.1f%%", $4}')"

printf '### Coverage — %s (total %s)\n\n' "$title" "$total"
printf '| Package | Stmts | Coverage |\n|---|---:|---:|\n'
printf '%s\n' "$rows" \
  | awk -F'\t' -v mod="$mod" '$1!="TOTAL"{ p=$1; sub("^"mod,"",p); printf "%s\t%d\t%s\n", p, $3, $4 }' \
  | sort \
  | awk -F'\t' '{ printf "| %s | %d | %s%% |\n", $1, $2, $3 }'
printf '%s\n' "$rows" | awk -F'\t' '$1=="TOTAL"{ printf "| **TOTAL** | **%d** | **%s%%** |\n", $3, $4 }'
