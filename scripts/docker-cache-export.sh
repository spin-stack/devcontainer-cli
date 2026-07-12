#!/usr/bin/env bash
# Exit 0 iff the Docker builder can export build cache (needed by the runtime
# parity matrix: build.cache-to-*, build.output-oci-*). The default `docker`
# driver cannot, so this fails fast (~1s) instead of ~4 min deep with a cryptic
# buildkit error. Enable the containerd image store or use a docker-container
# buildx builder (see scripts/ci-prepare-runner.sh).
set -euo pipefail

d="$(mktemp -d)"
trap 'rm -rf "$d"' EXIT
printf 'FROM busybox\n' > "$d/Dockerfile"
docker buildx build --cache-to=type=local,dest="$d/c" -t devcontainer-cache-export-probe "$d" >/dev/null 2>&1
