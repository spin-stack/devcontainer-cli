#!/usr/bin/env bash
# Decide whether the daily scheduled parity run should do the heavy Docker work:
# emit `run=<true|false>` (one line, for appending to $GITHUB_OUTPUT). It is `true`
# iff HEAD carries a commit within the look-back window — i.e. the repo actually
# changed since the previous daily run. On a private (billed) repo this skips the
# expensive full runtime matrix on quiet days.
#
# Usage: daily-changed.sh [window-hours]
#   window-hours  look-back in hours (default 25 — daily cron period + 1h jitter margin)
# Env:
#   DAILY_NOW_EPOCH  override "now" in unix seconds (tests use this for determinism)
set -euo pipefail

window_hours="${1:-25}"
now="${DAILY_NOW_EPOCH:-$(date +%s)}"
# %ct is the committer timestamp; -1 is HEAD, the newest commit on the branch.
last="$(git log -1 --format=%ct)"
age_hours=$(( (now - last) / 3600 ))

echo "HEAD commit is ${age_hours}h old (window ${window_hours}h)" >&2
if [ "$age_hours" -lt "$window_hours" ]; then
  echo "run=true"
else
  echo "run=false"
  echo "no commits within the last ${window_hours}h — skipping the full runtime matrix" >&2
fi
