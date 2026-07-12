#!/usr/bin/env bash
# Merge every per-lane coverage data directory under a root into one dataset and
# print the unified per-package table + module total. This is the true coverage of
# the code across ALL test lanes (unit + e2e + parity), which no single lane shows.
#
# Usage: coverage-merge.sh <data-root> [out-dir] [title]
#   <data-root>  dir containing per-lane subdirs of covdata (e.g. artifacts/coverage/data)
#   [out-dir]    where to write the merged covdata + merged.out (default <data-root>/merged)
#   [title]      report heading (default "merged (all lanes)")
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${1:?usage: coverage-merge.sh <data-root> [out-dir] [title]}"
out="${2:-${root%/}/merged}"
title="${3:-merged (all lanes)}"

# Collect non-empty lane dirs (skip the output dir itself).
inputs=""
for d in "$root"/*/; do
  d="${d%/}"
  [ "$d" = "$out" ] && continue
  [ -n "$(ls -A "$d" 2>/dev/null)" ] || continue
  inputs="${inputs:+$inputs,}$d"
done

if [ -z "$inputs" ]; then
  printf '### Coverage — %s\n\n_(no lane data to merge)_\n' "$title"
  exit 0
fi

rm -rf "$out" && mkdir -p "$out"
go tool covdata merge -i="$inputs" -o="$out"
exec "$here/coverage-report.sh" "$out" "$title"
