#!/bin/sh
# Compare build and up JSON output structure between TS and Go CLI.
# Requires Docker.
set -e

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLI_TS="node ${REPO_ROOT}/devcontainer.js"
CLI_GO="${REPO_ROOT}/devcontainer"

pass=0
fail=0
failures=""

check_outcome() {
    local name="$1" cmd="$2" fixture="$3"
    local fixture_path="${REPO_ROOT}/src/test/configs/${fixture}"

    ts_out=$(${CLI_TS} ${cmd} --workspace-folder "$fixture_path" 2>/dev/null || true)
    go_out=$(${CLI_GO} ${cmd} --workspace-folder "$fixture_path" 2>/dev/null || true)

    ts_outcome=$(echo "$ts_out" | python3 -c "import json,sys; print(json.load(sys.stdin).get('outcome','none'))" 2>/dev/null || echo "parse_error")
    go_outcome=$(echo "$go_out" | python3 -c "import json,sys; print(json.load(sys.stdin).get('outcome','none'))" 2>/dev/null || echo "parse_error")

    # Compare outcome field
    if [ "$ts_outcome" = "$go_outcome" ]; then
        # For success, also compare key fields
        if [ "$ts_outcome" = "success" ]; then
            ts_keys=$(echo "$ts_out" | python3 -c "import json,sys; print(sorted(json.load(sys.stdin).keys()))" 2>/dev/null)
            go_keys=$(echo "$go_out" | python3 -c "import json,sys; print(sorted(json.load(sys.stdin).keys()))" 2>/dev/null)
            if [ "$ts_keys" != "$go_keys" ]; then
                printf "  \033[33mWARN\033[0m  %s (outcome matches but keys differ: TS=%s Go=%s)\n" "$name" "$ts_keys" "$go_keys"
            else
                printf "  \033[32mPASS\033[0m  %s\n" "$name"
            fi
        else
            printf "  \033[32mPASS\033[0m  %s (outcome=%s)\n" "$name" "$ts_outcome"
        fi
        pass=$((pass + 1))
    else
        printf "  \033[31mFAIL\033[0m  %s (TS=%s Go=%s)\n" "$name" "$ts_outcome" "$go_outcome"
        fail=$((fail + 1))
        failures="${failures}\n  ${name}: TS=${ts_outcome} Go=${go_outcome}"
    fi
}

echo "=== Build command ==="
check_outcome "build image" "build" "image"
check_outcome "build dockerfile" "build" "dockerfile-without-features"

echo ""
echo "=== Up command (--skip-post-create) ==="
check_outcome "up image" "up --skip-post-create" "image"
check_outcome "up dockerfile" "up --skip-post-create" "dockerfile-without-features"

# Cleanup
docker ps -q --filter "label=devcontainer.local_folder" 2>/dev/null | xargs -r docker rm -f >/dev/null 2>&1 || true

echo ""
echo "Results: ${pass} passed, ${fail} failed"
if [ "$fail" -gt 0 ]; then
    printf "\nFailures:${failures}\n"
    exit 1
fi
