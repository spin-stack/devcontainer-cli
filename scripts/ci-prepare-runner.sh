#!/usr/bin/env bash
# CI-ONLY. Prepare a GitHub-hosted runner for the runtime parity / e2e lanes:
#
#   1. Free ~25-30GB by removing big preinstalled SDKs. The serial image builds
#      otherwise exhaust the root disk ("No space left on device") and the runner
#      dies hard, losing logs. /mnt is the SAME filesystem as / on these runners,
#      so moving docker there buys nothing — freeing / is what helps.
#   2. Enable the containerd image store, needed for build-cache export and
#      --platform (see scripts/docker-cache-export.sh).
#
# Uses sudo and restarts Docker — do NOT run on a developer machine.
set -euo pipefail

echo "disk before:"; df -h / | tail -1
sudo rm -rf /usr/share/dotnet /opt/ghc /usr/local/lib/android \
            /opt/hostedtoolcache/CodeQL /usr/share/swift \
            /usr/local/share/boost /usr/lib/jvm || true
sudo docker image prune -af >/dev/null 2>&1 || true
echo "disk after:";  df -h / | tail -1

echo '{"features":{"containerd-snapshotter":true}}' | sudo tee /etc/docker/daemon.json
sudo systemctl restart docker
docker info -f 'driver={{.DriverStatus}}'
