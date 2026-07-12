#!/usr/bin/env bash
# Map the change between two commits to the parity runtime commands it can affect,
# emitting `commands=<all|none|csv>` and `run=<true|false>` (one per line, for
# appending to $GITHUB_OUTPUT). An unknown/absent base (new branch, all-zero SHA)
# selects the full matrix.
#
# Usage: parity-affected-commands.sh <base-sha> <head-sha>
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
base="${1:-}"
head="${2:-HEAD}"

if [ -z "$base" ] || ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
  echo "unknown base → running the full matrix" >&2
  echo "commands=all"
  echo "run=true"
  exit 0
fi

files="$(git diff --name-only "$base" "$head")"
echo "changed files:" >&2
echo "$files" >&2

cmds="$(printf '%s\n' "$files" | "$here/parity-affected.sh")"
echo "affected parity commands: $cmds" >&2

echo "commands=$cmds"
if [ "$cmds" = none ]; then
  echo "run=false"
else
  echo "run=true"
fi
