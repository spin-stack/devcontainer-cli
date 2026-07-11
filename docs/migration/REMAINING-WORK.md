# Trabajo restante para paridad y release

Este documento es la **única fuente operativa del trabajo pendiente** para llevar
el CLI Go a paridad demostrada con `devcontainers/cli` v0.88.0. El estado resumido vive en
[`GO-REWRITE-STATUS.md`](GO-REWRITE-STATUS.md) y el gate final en
[`RELEASE-CHECKLIST.md`](RELEASE-CHECKLIST.md). El plan de ejecución por ítem
(esfuerzo, dependencias, paralelización con agentes) vive en
[`EXECUTION-PLAN.md`](EXECUTION-PLAN.md).

## Regla de cierre

Un ítem sólo se considera cerrado cuando:

1. el comportamiento está implementado o la divergencia fue descartada de forma
   explícita;
2. existe un test proporcional al riesgo;
3. cuando aplica, el test compara Go contra el oráculo TS;
4. CI ejecuta el test en el lane correcto;
5. la matriz y este documento se actualizan con evidencia, no anticipadamente.

## Estado (al día)

Cerrados o hechos: RW-001, RW-002, RW-003, RW-004, RW-007, RW-009, RW-010, RW-011,
RW-013, RW-015, RW-017. Parciales (falta cola de evidencia o CI con secrets): RW-005,
RW-006, RW-008, RW-014. **Pendientes reales:** **RW-012** (cobertura), **RW-016**
(imagen OCI) y **RW-018** (corrida limpia — gate final).

## P0 — Paridad funcional

### RW-001 — `overrideFeatureInstallOrder` en `up`/`build` — ✅ HECHO
`cfg.OverrideFeatureInstallOrder` se cablea hasta el builder unificado; rechaza
entradas inválidas como TS. Tests en `internal/features/graph_test.go`.

### RW-002 — Unificar el grafo de Features — ✅ HECHO
`features.BuildDependencyGraph` (seam `processFeature`) alimenta instalación,
`resolve-dependencies` y mermaid; arregla el bug de `resolve-dependencies` que
construía nodos **sin aristas**. Tests herméticos con stub en memoria.

### RW-003 — Contrato PTY/señales de `exec` — ✅ HECHO
`exec` usa `docker exec -it` heredado (equivalente al fallback sin node-pty de TS);
el código PTY muerto fue borrado; contrato `128+N` endurecido para el caso del
proceso host señalado. Decisión registrada en [`EXECUTION-PLAN.md`](EXECUTION-PLAN.md)
(RW-003 Branch A).

### RW-004 — `--docker-compose-path` — ✅ HECHO
Cableado end-to-end en `build` (único comando que faltaba; `up` ya lo tenía). Los
otros comandos con el flag no ejecutan compose (attach por labels), igual que TS. Test
discriminante en `internal/cli/build_compose_path_test.go`.

### RW-005 — Casos diferidos de la matriz — 🟡 PARCIAL
El assert de `features.test-single-scenario-success` se redujo a `[exit_code]` (su
stdout es ANSI no determinista, no comparable). **Pendiente:** la promoción
*evidence-based* de ambos casos (`build.buildkit-never-platform-failure` y el de
features-test) — correr con Docker/red en amd64 y flipear `current_status → match` a
partir del JSON artefactado. **Se ejecuta dentro de RW-018.**

## P1 — Compatibilidad de datos y plataformas

### RW-006 — Interop metadata TS↔Go — 🟡 HECHO (hermético)
Test de round-trip Go y de invariancia de whitespace (comparando JSON parseado, no
bytes) en `internal/cli/metadata_interop_test.go`. **Pendiente:** la mitad TS→Go
(construir con el oráculo TS, leer con Go) está *skip-guarded* hasta tener el oráculo
compilado → se valida en RW-018.

### RW-007 — OCI image indexes por plataforma — ✅ HECHO (retirado)
Los tipos muertos `ImageIndex`/`ImageIndexEntry`/`Platform` y
`OCIImageIndexMediaType` fueron retirados. v0.88.0 sólo usa resolución de índices en
`inspectImageInRegistry` (no portado). Decisión en [`EXECUTION-PLAN.md`](EXECUTION-PLAN.md):
si se porta ese path, implementar vía oras, no con structs a mano.

### RW-008 — Registries y credenciales reales — 🟡 HECHO (hermético)
Loop 401→bearer→pull/push contra `registry:3` con htpasswd, protocolo de credential
helper con fake en PATH, `secretservice` fijado (no el `secret` erróneo de TS), y
cache de auth compartido en `oci.Client`. Tests en `internal/oci/`. **Pendiente:** la
matriz cloud real (ACR identity/refresh, ECR helper, GHCR autenticado) **gated por
secrets** en CI, no bloqueante. Helpers sólo Linux (`secretservice`/`pass`).

### RW-009 — Podman y Compose v1 — ✅ CERRADO: no soportado
El CLI Go soporta **sólo Docker** y **sólo `docker compose` v2**. Podman y Compose v1
**no se soportan** — divergencia deliberada, sin garantía ni test de paridad.

### RW-010 — Paths y ejecución Windows — ✅ CERRADO: no soportado
El CLI Go se soporta **sólo en Linux** (amd64/arm64). Windows y macOS no son
objetivos: sin runtime/E2E/release/lane `windows-latest`/ConPTY. La lógica
`platform="win32"` se conserva sólo por paridad con el oráculo TS.

## P2 — Calidad orientada a riesgo

### RW-011 — Seams para efectos externos — ✅ HECHO
Cuatro interfaces pequeñas (`cli.Output`, `oci.Registry`, `exec.Runner`, `pfs.FS`),
sin `CLIHost` monolítico. Tests con fakes (publish parcial, runner). Decisión en
[`EXECUTION-PLAN.md`](EXECUTION-PLAN.md).

### RW-012 — Cobertura de paths críticos — ❌ PENDIENTE

Baseline unitario aproximado (previo a RW-011; algunos números ya mejoraron):

| Paquete | Cobertura |
|---|---:|
| CLI | 21.1% |
| OCI | 43.2% |
| lifecycle | 41.9% |
| templates | 47.5% |
| Docker | 61.1% |
| features | 73.0% |
| imagemeta | 74.3% |
| config | 78.6% |

No se exige subir números mediante tests triviales. Prioridad (apoyada en los seams de
RW-011, ya disponibles):

- errores entre capas y cancelación;
- cleanup tras fallos parciales;
- publish parcial (fake `oci.Registry`);
- auth/retries OCI (registry httptest);
- shell server y user env probe real (extraer un `shellExec` inyectable);
- templates con workspace parcialmente escrito (fake `pfs.FS` con `WriteFile` que falla);
- Docker/Compose argument construction.

**Aceptación:** cada incremento cubre un riesgo nombrado. El objetivo de referencia de
80% por paquete se mantiene como dirección, no como sustituto de paridad E2E.

### RW-013 — Validar inventario de flags automáticamente — ✅ HECHO
`TestFlagInventoryParity` camina el árbol Cobra y lo diffea contra
`cli-flags-inventory.yaml` (0 drift; CI falla ante drift). Además destapó y corrigió
bugs reales de `hidden`/alias (`skip-feature-auto-mapping` y experimentales en
`up`/`run-user-commands`/`exec`; `-f`/`-v` + hidden en `upgrade`).

### RW-014 — Completar contratos de HTTP y host — 🟡 PARCIAL

**Hecho:** transporte HTTP compartido (`httpx.NewTransport`) usado por **todos** los
paths (httpx, OCI/oras vía `retry.NewTransport`, y descarga de tarballs). Honra
`HTTP(S)_PROXY`/`NO_PROXY` leyendo el entorno **fresco por request**
(`golang.org/x/net/http/httpproxy`, sin el `sync.Once` de `http.ProxyFromEnvironment`)
y carga CA extra (`NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE`) — antes el path OCI y el de
descarga no cargaban la CA, por lo que un proxy con intercepción TLS rompía los pulls
(síntoma: "no respeta el proxy"). Tests herméticos de selección de proxy, ruteo real y
confianza de CA end-to-end.

**Trabajo restante:** redirects (política `CheckRedirect`), cancelación por contexto en
`httpx.Do` (`http.NewRequestWithContext`) y errores de proceso/filesystem
multiplataforma, apoyándose en los seams pequeños de RW-011. Además, los flags
`--log-file`/`--terminal-log-file` se aceptan por paridad de superficie pero **no
están cableados** para escribir logs a archivo (el stub muerto `setupLogFile` fue
eliminado); falta implementarlos (tee de stderr al archivo) o marcarlos como no
soportados.

## P3 — Release y operación

### RW-015 — Pipeline GoReleaser — ✅ HECHO
`.goreleaser.yml` sin `go test ./...` en el hook, matriz reducida a Linux
(amd64/arm64), bloque `sboms:`, y workflow `release.yml` por tag que corre los gates
de CI y produce draft release con checksums/SBOM. **Nota:** `goreleaser`/`syft` no
están instalados localmente, así que la config no fue verificada con
`task release -- --snapshot`; se valida en un runner con las herramientas (RW-018).

### RW-016 — Distribuir imagen OCI del CLI — ❌ PENDIENTE

**Trabajo:** imagen mínima multi-arch (linux/amd64+arm64) con el binario estático
(`distroless/static:nonroot`, que incluye CA certs), labels OCI (`version`/`source`/
`revision`), provenance/SBOM, y smoke test `docker run <image> --version`. Preferible
vía `dockers:`/`docker_manifests:` de GoReleaser (reusa los binarios ya construidos).

**Decisión abierta:** nombre/registry de la imagen (probable `ghcr.io/devcontainers/cli`).

**Aceptación:** `docker run <image> --version`, amd64/arm64, digest artefactado.

### RW-017 — Métricas de rendimiento y distribución — ✅ HECHO

`task metrics` (Taskfile) emite `artifacts/metrics.json` y el job `metrics` de
`.github/workflows/release.yml` lo captura como artefacto. Usa `hyperfine` si está en
el runner, con fallback a un loop `date +%s%N` promediado (`METRICS_RUNS`, default
30). El task es **no-gating** (`ignore_error: true`; el job usa `continue-on-error`).

**Métricas capturadas** (`metrics.json`): `startup_ms.{go_version,go_read_configuration,
node_version}`; `sizes_bytes.{local_binary,linux_amd64_binary,linux_amd64_gzip,
linux_arm64_binary,linux_arm64_gzip}`; metadatos `timing_tool/runs/version/generated_at`.

**Aceptación:** cada release produce `metrics.json` con todos los campos no nulos en un
runner con Docker + oráculo Node compilado, registrado en `GO-REWRITE-STATUS.md`.

**Regresión aceptada (registrada, NO gated):** base = primera corrida limpia sobre el
commit candidato de RW-018. Se anota —sin frenar el release— startup > 1.5× base o peor
que el oráculo Node, binario > 1.2× base, o gzip > 1.2× base. Cruzar el umbral exige
una nota en `GO-REWRITE-STATUS.md`; no invalida el release.

### RW-018 — Corrida limpia de paridad v0.88.0 — ❌ PENDIENTE (gate final)

Ejecutar en el commit candidato, en un runner con Docker + red + oráculo compilado:

```sh
task lint && task coverage && task test:integration && task test:e2e
task reference
task parity:contract && task parity:network && task parity:runtime
task build:cross
```

Incluye resolver las colas de otros ítems: promover los diferidos de **RW-005**
(evidence-based), correr la mitad TS→Go de **RW-006**, y verificar `task release --
--snapshot` de **RW-015** con `goreleaser`/`syft` instalados.

**Aceptación:** cero `failed`, cero `inconclusive`, deferred resueltos, SHA del
oráculo y JSON de cada lane guardados, checklist completa. Bloqueado por RW-012
(alimenta `task coverage`) y por las colas de RW-005/006.

## Decisiones que deben quedar explícitas

Decisiones ya tomadas (firmes):

- **Plataforma: sólo Linux** (amd64/arm64). Sin Windows ni macOS. → RW-010 cerrado.
- **Runtime: sólo Docker.** Podman no soportado. → RW-009 cerrado.
- **Compose: sólo v2** (`docker compose`). Compose v1 no soportado. → RW-009 cerrado.
- **exec: terminal heredado** (`docker exec -it`), sin PTY propio. → RW-003 Branch A.

Puntos que aún no deben permanecer ambiguos:

- `--log-file`/`--terminal-log-file`: implementar (tee a archivo) o marcar no soportado (RW-014);
- nombre/registry de la imagen OCI del CLI (RW-016);
- fallback de legacy Features por GitHub Releases;
- paridad byte-a-byte del tarball, hoy no alcanzable por `mtime`;
- alcance de ACR/ECR en CI regular o programado (helpers sólo Linux: `secretservice`/`pass`).

Una decisión de no soportar un comportamiento cierra el ítem sólo si se documenta
como divergencia deliberada, se retira la surface engañosa y existe un test del
contrato elegido.
