#!/usr/bin/env bash
# Enforce coverage floors against a Go cover profile. Fails (exit 1) if the module
# total or any named package is below its threshold, printing every check so the
# CI log shows exactly which floor moved.
#
# Usage: coverage-gate.sh <profile.out> TOTAL=<min> [<pkg>=<min> ...]
#   e.g. coverage-gate.sh coverage.out TOTAL=48 cli=28 docker=77 oci=85
#
# Package keys are matched under github.com/devcontainers/cli/internal/<pkg>.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mod="github.com/devcontainers/cli/internal/"

profile="${1:?usage: coverage-gate.sh <profile.out> TOTAL=<min> [pkg=min ...]}"
shift

rows="$(awk -f "$here/pkgcov.awk" "$profile")"

# pct <key> -> weighted % for TOTAL or internal/<key>, or empty if absent.
pct() {
  local key="$1" match
  if [ "$key" = TOTAL ]; then match="TOTAL"; else match="${mod}${key}"; fi
  printf '%s\n' "$rows" | awk -F'\t' -v m="$match" '$1==m{print $4; found=1} END{if(!found) exit 0}'
}

rc=0
for spec in "$@"; do
  key="${spec%%=*}"; min="${spec##*=}"
  got="$(pct "$key")"
  if [ -z "$got" ]; then
    printf 'FAIL: no coverage data for %s\n' "$key"; rc=1; continue
  fi
  if awk -v g="$got" -v m="$min" 'BEGIN{exit !(g+0 < m+0)}'; then
    printf 'FAIL: %s coverage %s%% is below the %s%% floor\n' "$key" "$got" "$min"; rc=1
  else
    printf 'OK:   %s coverage %s%% >= %s%% floor\n' "$key" "$got" "$min"
  fi
done
exit "$rc"
