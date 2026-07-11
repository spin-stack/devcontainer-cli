#!/usr/bin/env bash
# =============================================================================
# Golden Test Harness para migración @devcontainers/cli TS → Go
#
# Captura stdout, stderr y exit code del CLI TS actual contra fixtures,
# para después comparar byte-a-byte con el CLI Go.
#
# Uso:
#   ./golden-test-harness.sh capture   # Genera snapshots del CLI TS
#   ./golden-test-harness.sh compare   # Compara CLI Go vs snapshots
#   ./golden-test-harness.sh list      # Lista fixtures disponibles
#
# Requisitos:
#   - Node.js >= 18
#   - Docker (para tests que lo requieran)
#   - jq (para normalización de JSON)
#   - El CLI TS compilado (yarn compile)
# =============================================================================
set -euo pipefail

# ─── Configuración ──────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
FIXTURES_DIR="${REPO_ROOT}/src/test/configs"
GOLDEN_DIR="${REPO_ROOT}/docs/migration/golden-snapshots"

# CLI binarios
CLI_TS="${REPO_ROOT}/devcontainer.js"
CLI_GO="${CLI_GO:-devcontainer}"  # Overrideable via env

# Timeout por test (segundos)
TIMEOUT="${GOLDEN_TIMEOUT:-120}"

# ─── Utilidades ─────────────────────────────────────────────────────────────

log_info()  { echo -e "\033[36m[INFO]\033[0m  $*"; }
log_ok()    { echo -e "\033[32m[OK]\033[0m    $*"; }
log_fail()  { echo -e "\033[31m[FAIL]\033[0m  $*"; }
log_skip()  { echo -e "\033[33m[SKIP]\033[0m  $*"; }

normalize_json() {
  # Normaliza JSON para comparación estable:
  # - Ordena keys
  # - Elimina campos volátiles (containerId, timestamps, paths absolutos del host)
  jq -S '
    # Eliminar campos que cambian entre ejecuciones
    walk(
      if type == "object" then
        del(.containerId, .composeProjectName) |
        with_entries(
          if (.key | test("^/|^[A-Z]:\\\\")) then empty  # paths absolutos
          else .
          end
        )
      else .
      end
    )
  ' 2>/dev/null || cat  # Fallback si no es JSON válido
}

normalize_stderr() {
  # Normaliza stderr para comparación estable:
  # - Reemplaza timestamps [123 ms] con [X ms]
  # - Reemplaza version strings (devcontainer-cli 0.74.0 → devcontainer-cli X.Y.Z)
  # - Reemplaza paths absolutos del host con <HOST_PATH>
  # - Reemplaza container IDs (sha256 hex) con <CONTAINER_ID>
  # - Reemplaza image hashes con <IMAGE_HASH>
  sed -E \
    -e 's/\[[0-9]+ ms\]/[X ms]/g' \
    -e 's/dev-containers-cli [0-9]+\.[0-9]+\.[0-9]+/dev-containers-cli X.Y.Z/g' \
    -e 's|/Users/[^ ]+|<HOST_PATH>|g' \
    -e 's|/home/[^ ]+|<HOST_PATH>|g' \
    -e 's|/tmp/[^ ]+|<TMP_PATH>|g' \
    -e 's/sha256:[a-f0-9]{64}/sha256:<HASH>/g' \
    -e 's/[a-f0-9]{12,64}/<CONTAINER_ID>/g' \
    2>/dev/null || cat
}

# Ejecuta un comando CLI y captura stdout, stderr, exit code
run_cli() {
  local cli_bin="$1"
  shift
  local stdout_file="$1"
  local stderr_file="$2"
  local exitcode_file="$3"
  shift 3

  local exit_code=0
  timeout "${TIMEOUT}" "${cli_bin}" "$@" \
    >"${stdout_file}" \
    2>"${stderr_file}" \
    || exit_code=$?

  echo "${exit_code}" > "${exitcode_file}"
}

# ─── Test cases ─────────────────────────────────────────────────────────────
# Cada test case define:
#   - Un nombre único
#   - El comando y flags a ejecutar
#   - Si requiere Docker
#   - Si el stdout es JSON (para normalización)

declare -A TEST_CASES

# --- read-configuration tests (NO requieren Docker) ---
define_read_config_tests() {
  local fixtures=(
    image
    image-with-features
    image-metadata
    image-metadata-containerEnv
    dockerfile-without-features
    dockerfile-with-features
    dockerfile-with-target
    dockerfile-with-syntax
    compose-image-without-features
    compose-image-without-features-minimal
    compose-Dockerfile-without-features
    compose-Dockerfile-with-features
    compose-Dockerfile-with-target
    compose-with-name
    compose-without-name
  )

  for fixture in "${fixtures[@]}"; do
    local name="read-config--${fixture}"
    TEST_CASES["${name}"]="no-docker|json|read-configuration --workspace-folder ${FIXTURES_DIR}/${fixture}"
  done

  # Con --include-merged-configuration (requiere Docker para image inspect)
  for fixture in image image-with-features; do
    local name="read-config-merged--${fixture}"
    TEST_CASES["${name}"]="docker|json|read-configuration --workspace-folder ${FIXTURES_DIR}/${fixture} --include-merged-configuration"
  done

  # Con --include-features-configuration
  for fixture in image-with-features dockerfile-with-features; do
    local name="read-config-features--${fixture}"
    TEST_CASES["${name}"]="no-docker|json|read-configuration --workspace-folder ${FIXTURES_DIR}/${fixture} --include-features-configuration"
  done
}

# --- build tests (requieren Docker) ---
define_build_tests() {
  local fixtures=(
    image
    image-with-features
    dockerfile-without-features
    dockerfile-with-features
    dockerfile-with-target
  )

  for fixture in "${fixtures[@]}"; do
    local name="build--${fixture}"
    TEST_CASES["${name}"]="docker|json|build --workspace-folder ${FIXTURES_DIR}/${fixture}"
  done

  # build with --no-cache
  TEST_CASES["build-nocache--image"]="docker|json|build --workspace-folder ${FIXTURES_DIR}/image --no-cache"
}

# --- up tests (requieren Docker) ---
define_up_tests() {
  local fixtures=(
    image
    dockerfile-without-features
    dockerfile-with-features
    image-with-features
  )

  for fixture in "${fixtures[@]}"; do
    local name="up--${fixture}"
    TEST_CASES["${name}"]="docker|json|up --workspace-folder ${FIXTURES_DIR}/${fixture} --skip-post-create"
  done

  # up con compose
  for fixture in compose-image-without-features compose-Dockerfile-without-features; do
    local name="up-compose--${fixture}"
    TEST_CASES["${name}"]="docker|json|up --workspace-folder ${FIXTURES_DIR}/${fixture} --skip-post-create"
  done

  # up con --include-configuration y --include-merged-configuration
  TEST_CASES["up-with-config--image"]="docker|json|up --workspace-folder ${FIXTURES_DIR}/image --skip-post-create --include-configuration --include-merged-configuration"
}

# --- set-up tests (requieren Docker) ---
define_set_up_tests() {
  # set-up requiere un container existente; capturamos el error esperado con container-id falso
  TEST_CASES["set-up--missing-container"]="docker|json|set-up --container-id nonexistent_container_12345"
}

# --- run-user-commands tests (requieren Docker) ---
define_run_user_commands_tests() {
  # Capturamos el error esperado sin container
  TEST_CASES["run-user-commands--no-container"]="docker|json|run-user-commands --workspace-folder ${FIXTURES_DIR}/image --container-id nonexistent_container_12345"
}

# --- exec tests (requieren Docker) ---
define_exec_tests() {
  # exec sin container existente → error
  TEST_CASES["exec--no-container"]="docker|text|exec --workspace-folder ${FIXTURES_DIR}/image echo hello"
}

# --- outdated tests (no requieren Docker, pero sí features en config) ---
define_outdated_tests() {
  TEST_CASES["outdated--image-with-features"]="no-docker|text|outdated --workspace-folder ${FIXTURES_DIR}/image-with-features --output-format json"
}

# --- upgrade tests ---
define_upgrade_tests() {
  TEST_CASES["upgrade--dry-run"]="no-docker|json|upgrade --workspace-folder ${FIXTURES_DIR}/image-with-features --dry-run"
}

# --- features subcommand tests ---
define_features_tests() {
  # features info (no requiere Docker)
  TEST_CASES["features-info--manifest"]="no-docker|json|features info manifest ghcr.io/devcontainers/features/go"
  TEST_CASES["features-info--tags"]="no-docker|json|features info tags ghcr.io/devcontainers/features/go"
}

# --- templates subcommand tests ---
define_templates_tests() {
  # templates metadata (no requiere Docker)
  TEST_CASES["templates-metadata"]="no-docker|json|templates metadata ghcr.io/devcontainers/templates/javascript-node"
}

# --- Cargar todos los test cases ---
load_test_cases() {
  define_read_config_tests
  define_build_tests
  define_up_tests
  define_set_up_tests
  define_run_user_commands_tests
  define_exec_tests
  define_outdated_tests
  define_upgrade_tests
  define_features_tests
  define_templates_tests
}

# ─── Subcomando: list ────────────────────────────────────────────────────────
cmd_list() {
  load_test_cases
  log_info "Test cases disponibles (${#TEST_CASES[@]} total):"
  echo ""
  printf "  %-45s %s\n" "NOMBRE" "REQUIERE DOCKER"
  printf "  %-45s %s\n" "------" "---------------"
  for name in $(echo "${!TEST_CASES[@]}" | tr ' ' '\n' | sort); do
    local spec="${TEST_CASES[$name]}"
    local docker_req
    docker_req=$(echo "$spec" | cut -d'|' -f1)
    printf "  %-45s %s\n" "${name}" "${docker_req}"
  done
}

# ─── Subcomando: capture ─────────────────────────────────────────────────────
cmd_capture() {
  local filter="${1:-}"
  local skip_docker="${GOLDEN_SKIP_DOCKER:-false}"

  load_test_cases
  mkdir -p "${GOLDEN_DIR}"

  log_info "Capturando golden snapshots con CLI TS..."
  log_info "Directorio: ${GOLDEN_DIR}"
  echo ""

  local total=0
  local captured=0
  local skipped=0
  local failed=0

  for name in $(echo "${!TEST_CASES[@]}" | tr ' ' '\n' | sort); do
    total=$((total + 1))

    # Filtro opcional
    if [[ -n "${filter}" ]] && [[ "${name}" != *"${filter}"* ]]; then
      continue
    fi

    local spec="${TEST_CASES[$name]}"
    local docker_req is_json cmd_args
    docker_req=$(echo "$spec" | cut -d'|' -f1)
    is_json=$(echo "$spec" | cut -d'|' -f2)
    cmd_args=$(echo "$spec" | cut -d'|' -f3-)

    # Skip si requiere Docker y no está disponible
    if [[ "${docker_req}" == "docker" ]]; then
      if [[ "${skip_docker}" == "true" ]] || ! docker info &>/dev/null; then
        log_skip "${name} (requiere Docker)"
        skipped=$((skipped + 1))
        continue
      fi
    fi

    local test_dir="${GOLDEN_DIR}/${name}"
    mkdir -p "${test_dir}"

    log_info "Capturando: ${name}..."

    # shellcheck disable=SC2086
    run_cli "node" \
      "${test_dir}/stdout.raw" \
      "${test_dir}/stderr.raw" \
      "${test_dir}/exitcode" \
      "${CLI_TS}" ${cmd_args}

    # Normalizar JSON si aplica
    if [[ "${is_json}" == "json" ]]; then
      normalize_json < "${test_dir}/stdout.raw" > "${test_dir}/stdout.json" 2>/dev/null || true
    fi

    # Guardar metadata del test
    cat > "${test_dir}/metadata.json" <<EOF
{
  "name": "${name}",
  "command": "devcontainer ${cmd_args}",
  "requires_docker": ${docker_req/no-docker/false},
  "json_output": ${is_json/json/true},
  "captured_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "cli_version": "$(node "${CLI_TS}" --version 2>/dev/null || echo unknown)"
}
EOF

    local exit_code
    exit_code=$(cat "${test_dir}/exitcode")
    if [[ "${exit_code}" == "0" ]]; then
      log_ok "${name} (exit ${exit_code})"
      captured=$((captured + 1))
    else
      log_fail "${name} (exit ${exit_code})"
      failed=$((failed + 1))
    fi
  done

  echo ""
  log_info "Resumen: ${captured} capturados, ${skipped} saltados, ${failed} fallidos (de ${total} total)"
  log_info "Snapshots en: ${GOLDEN_DIR}"
}

# ─── Subcomando: compare ─────────────────────────────────────────────────────
cmd_compare() {
  local filter="${1:-}"
  local skip_docker="${GOLDEN_SKIP_DOCKER:-false}"

  load_test_cases

  if [[ ! -d "${GOLDEN_DIR}" ]]; then
    log_fail "No hay snapshots. Ejecuta primero: $0 capture"
    exit 1
  fi

  log_info "Comparando CLI Go vs golden snapshots..."
  echo ""

  local total=0
  local passed=0
  local failed=0
  local skipped=0
  local failures=()

  for name in $(echo "${!TEST_CASES[@]}" | tr ' ' '\n' | sort); do
    local test_dir="${GOLDEN_DIR}/${name}"
    [[ -d "${test_dir}" ]] || continue

    # Filtro opcional
    if [[ -n "${filter}" ]] && [[ "${name}" != *"${filter}"* ]]; then
      continue
    fi

    total=$((total + 1))

    local spec="${TEST_CASES[$name]}"
    local docker_req is_json cmd_args
    docker_req=$(echo "$spec" | cut -d'|' -f1)
    is_json=$(echo "$spec" | cut -d'|' -f2)
    cmd_args=$(echo "$spec" | cut -d'|' -f3-)

    if [[ "${docker_req}" == "docker" ]]; then
      if [[ "${skip_docker}" == "true" ]] || ! docker info &>/dev/null; then
        log_skip "${name}"
        skipped=$((skipped + 1))
        continue
      fi
    fi

    # Ejecutar CLI Go
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # shellcheck disable=SC2086
    run_cli "${CLI_GO}" \
      "${tmp_dir}/stdout.raw" \
      "${tmp_dir}/stderr.raw" \
      "${tmp_dir}/exitcode" \
      ${cmd_args}

    # Comparar exit code
    local expected_exit actual_exit
    expected_exit=$(cat "${test_dir}/exitcode")
    actual_exit=$(cat "${tmp_dir}/exitcode")

    if [[ "${expected_exit}" != "${actual_exit}" ]]; then
      log_fail "${name}: exit code ${actual_exit} (expected ${expected_exit})"
      failures+=("${name}: exit code mismatch (got=${actual_exit}, want=${expected_exit})")
      failed=$((failed + 1))
      rm -rf "${tmp_dir}"
      continue
    fi

    # Comparar JSON output (normalizado)
    if [[ "${is_json}" == "json" ]] && [[ -f "${test_dir}/stdout.json" ]]; then
      normalize_json < "${tmp_dir}/stdout.raw" > "${tmp_dir}/stdout.json" 2>/dev/null || true

      if ! diff -q "${test_dir}/stdout.json" "${tmp_dir}/stdout.json" &>/dev/null; then
        log_fail "${name}: JSON output differs"
        echo "  --- diff ---"
        diff -u "${test_dir}/stdout.json" "${tmp_dir}/stdout.json" | head -30 || true
        echo "  --- end diff ---"
        failures+=("${name}: JSON output mismatch")
        failed=$((failed + 1))
        rm -rf "${tmp_dir}"
        continue
      fi
    fi

    # Comparar stderr normalizado (log structure)
    # Normalizamos timestamps, paths absolutos y version strings antes de comparar.
    if [[ -f "${test_dir}/stderr.raw" ]] && [[ -s "${test_dir}/stderr.raw" ]]; then
      normalize_stderr < "${test_dir}/stderr.raw" > "${test_dir}/stderr.norm" 2>/dev/null || true
      normalize_stderr < "${tmp_dir}/stderr.raw" > "${tmp_dir}/stderr.norm" 2>/dev/null || true

      if ! diff -q "${test_dir}/stderr.norm" "${tmp_dir}/stderr.norm" &>/dev/null; then
        # stderr mismatch es warning, no failure, a menos que GOLDEN_STRICT_STDERR=true
        if [[ "${GOLDEN_STRICT_STDERR:-false}" == "true" ]]; then
          log_fail "${name}: stderr output differs"
          failures+=("${name}: stderr mismatch")
          failed=$((failed + 1))
          rm -rf "${tmp_dir}"
          continue
        else
          log_info "${name}: stderr differs (non-fatal, use GOLDEN_STRICT_STDERR=true to enforce)"
        fi
      fi
    fi

    log_ok "${name}"
    passed=$((passed + 1))
    rm -rf "${tmp_dir}"
  done

  echo ""
  log_info "Resumen: ${passed} pasaron, ${failed} fallaron, ${skipped} saltados (de ${total} total)"

  if [[ ${#failures[@]} -gt 0 ]]; then
    echo ""
    log_fail "Tests fallidos:"
    for f in "${failures[@]}"; do
      echo "  - ${f}"
    done
    exit 1
  fi
}

# ─── Main ────────────────────────────────────────────────────────────────────
case "${1:-help}" in
  capture)
    cmd_capture "${2:-}"
    ;;
  compare)
    cmd_compare "${2:-}"
    ;;
  list)
    cmd_list
    ;;
  help|--help|-h)
    echo "Uso: $0 <capture|compare|list> [filtro]"
    echo ""
    echo "  capture [filtro]  Captura snapshots del CLI TS actual"
    echo "  compare [filtro]  Compara CLI Go vs snapshots capturados"
    echo "  list              Lista test cases disponibles"
    echo ""
    echo "Variables de entorno:"
    echo "  CLI_GO                Ruta al binario Go (default: devcontainer)"
    echo "  GOLDEN_SKIP_DOCKER    Saltar tests que requieren Docker (default: false)"
    echo "  GOLDEN_TIMEOUT        Timeout por test en segundos (default: 120)"
    echo "  GOLDEN_STRICT_STDERR  Fallar si stderr difiere (default: false)"
    ;;
  *)
    echo "Subcomando desconocido: $1"
    echo "Usa: $0 help"
    exit 1
    ;;
esac
