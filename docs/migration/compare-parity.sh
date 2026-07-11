#!/bin/sh
# Quick parity comparison: TS CLI vs Go CLI read-configuration output.
# Does not require bash 4+.
set -e

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLI_TS="node ${REPO_ROOT}/reference/devcontainer.js"
CLI_GO="${REPO_ROOT}/devcontainer"
FIXTURES_DIR="${REPO_ROOT}/src/test/configs"

pass=0
fail=0
skip=0
failures=""

for fixture_dir in "${FIXTURES_DIR}"/*/; do
    name="$(basename "$fixture_dir")"

    # Check if fixture has a devcontainer.json
    if [ ! -f "${fixture_dir}.devcontainer/devcontainer.json" ] && [ ! -f "${fixture_dir}.devcontainer.json" ]; then
        skip=$((skip + 1))
        continue
    fi

    # Run TS CLI
    ts_out=$(${CLI_TS} read-configuration --workspace-folder "$fixture_dir" 2>/dev/null || echo '{"_error":"ts_failed"}')
    # Run Go CLI
    go_out=$(${CLI_GO} read-configuration --workspace-folder "$fixture_dir" 2>/dev/null || echo '{"_error":"go_failed"}')

    # Normalize: sort keys, remove volatile fields
    normalize='
import json, sys
try:
    d = json.load(sys.stdin)
    if "configuration" in d:
        c = d["configuration"]
        # Remove configFilePath (absolute path / URI object)
        c.pop("configFilePath", None)
        # Remove null values (TS omits them)
        for k in list(c.keys()):
            if c[k] is None:
                del c[k]
        # Remove empty objects that one side might omit
        for k in list(c.keys()):
            if isinstance(c[k], dict) and "$mid" in c[k]:
                del c[k]
    print(json.dumps(d, sort_keys=True))
except: print("PARSE_ERROR")
'
    ts_norm=$(echo "$ts_out" | python3 -c "$normalize" 2>/dev/null)
    go_norm=$(echo "$go_out" | python3 -c "$normalize" 2>/dev/null)

    if [ "$ts_norm" = "$go_norm" ]; then
        printf "  \033[32mPASS\033[0m  %s\n" "$name"
        pass=$((pass + 1))
    elif [ "$ts_norm" = "PARSE_ERROR" ] || [ "$go_norm" = "PARSE_ERROR" ]; then
        printf "  \033[33mSKIP\033[0m  %s (parse error)\n" "$name"
        skip=$((skip + 1))
    else
        printf "  \033[31mFAIL\033[0m  %s\n" "$name"
        fail=$((fail + 1))
        failures="${failures}\n--- ${name} ---"
        failures="${failures}\nTS:  ${ts_norm}"
        failures="${failures}\nGo:  ${go_norm}"
    fi
done

echo ""
echo "Results: ${pass} passed, ${fail} failed, ${skip} skipped"

if [ "$fail" -gt 0 ]; then
    echo ""
    echo "=== FAILURES ==="
    printf "$failures\n"
    exit 1
fi
