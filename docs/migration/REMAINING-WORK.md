# Trabajo restante para paridad y release

Este documento es la **única fuente operativa del trabajo pendiente** para llevar
el CLI Go a paridad demostrada con `devcontainers/cli` v0.88.0. El estado resumido vive en
[`GO-REWRITE-STATUS.md`](GO-REWRITE-STATUS.md) y el gate final en
[`RELEASE-CHECKLIST.md`](RELEASE-CHECKLIST.md).

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
RW-012, RW-013, RW-014, RW-015, RW-016, RW-017. Parciales (falta cola de evidencia o
CI con secrets): RW-005, RW-006, RW-008. **Pendiente real:** **RW-018** (corrida
limpia — gate final).

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
proceso host señalado. Decisión: herencia directa (no PTY propio), equivalente al
fallback de TS cuando node-pty no está — ver `exec.go` y la sección de decisiones.

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
`inspectImageInRegistry` (no portado). Decisión: si en el futuro se porta ese path,
implementarlo vía oras, no con structs de índice a mano.

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
sin `CLIHost` monolítico (decisión deliberada). Tests con fakes (publish parcial, runner).

### RW-012 — Cobertura de paths críticos — ✅ HECHO (riesgos nombrados)

Cobertura de los riesgos nombrados, cada test atado a un riesgo (no padding),
apoyada en los seams de RW-011. Deltas por paquete:

- `internal/oci` 75.9% → **86.1%** (publish parcial sin rollback, loop 401→bearer→token→retry, branches de credential helper);
- `internal/docker` 63.3% → **79.7%** (buildArgs/runArgs/compose args exactos vía `exec.Runner`);
- `internal/lifecycle` 39.5% → **60.2%** (seam `shellExec`; probe cache/timeout-124/PATH-merge; parsers 100%);
- `internal/templates` 46.5% → **88.9%** (workspace a medio escribir vía fake `pfs.FS`; errores fetch/merge);
- `internal/cli` — errores cross-layer pre-Docker en `runBuild`/`runUp`/`runExec`.

**Pendiente menor:** la **cancelación por contexto** en los runners de comandos
necesita un seam de contexto / rewiring (fuera del alcance de estos tracks); anotado
para un incremento futuro. Los paths sólo-Docker (p. ej. cleanup del Dockerfile temporal)
quedan cubiertos por E2E, no herméticamente.

Baseline unitario aproximado (previo a RW-011; ya superado en varios paquetes):

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

### RW-014 — Completar contratos de HTTP y host — ✅ HECHO

**Hecho:** transporte HTTP compartido (`httpx.NewTransport`) usado por **todos** los
paths (httpx, OCI/oras vía `retry.NewTransport`, y descarga de tarballs). Honra
`HTTP(S)_PROXY`/`NO_PROXY` leyendo el entorno **fresco por request**
(`golang.org/x/net/http/httpproxy`, sin el `sync.Once` de `http.ProxyFromEnvironment`)
y carga CA extra (`NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE`) — antes el path OCI y el de
descarga no cargaban la CA, por lo que un proxy con intercepción TLS rompía los pulls
(síntoma: "no respeta el proxy"). Tests herméticos de selección de proxy, ruteo real y
confianza de CA end-to-end.

**Contexto y redirects en `httpx.Do`:** la firma pasó a `Do(ctx, opts)`
(`http.NewRequestWithContext`), así una cancelación/deadline de contexto aborta la
request (el único caller, `GetControlManifest` → `enforceDisallowedFeatures`, propaga
el `ctx` del comando). Se expone `Client.SetCheckRedirect` para instalar una política
de redirects (el default de Go sigue hasta 10 saltos). Tests herméticos con `httptest`:
cadena de redirects multi-salto seguida entera, `ErrUseLastResponse` corta la cadena, y
contexto cancelado / con deadline aborta la request.

**log-file (tee a archivo, implementado):** `--log-file` está cableado — cuando se
setea, el writer del logger pasa a ser `io.MultiWriter(os.Stderr, file)` (helper
`logWriter` en `internal/cli/logfile.go`), con cierre del archivo vía `defer`. Cableado
en los comandos que exponen el flag según el inventario de paridad: `up`, `set-up`,
`run-user-commands`, `read-configuration`, `outdated`, `upgrade` y `exec` (`build` **no**
lo expone en v0.88.0, así que se deja fuera). Un error al abrir el archivo se reporta
(no se descartan logs en silencio) y cae de vuelta a `os.Stderr`. Test hermético
(`logfile_test.go`) que asegura que una línea de log aterriza en el archivo.

**`--terminal-log-file` (divergencia documentada):** en v0.88.0 el flag distingue el
stream terminal (con ANSI) del plano; el CLI Go mantiene **un solo stream de log** sin
PTY/terminal auto-gestionado (RW-003 Rama A: `exec` hereda la terminal), así que no hay
un stream terminal-formateado distinto que capturar. Por eso `--terminal-log-file`
también se teea al mismo stream combinado (nunca es un agujero negro), documentando que
ambos flags capturan la misma salida (sin ANSI). Divergencia deliberada del CLI TS, que
produce dos archivos con formatos distintos.

**Errores de proceso/filesystem:** tests herméticos de propagación apoyados en los seams
de RW-011 — `docker.Client.Run` envuelve y propaga un fallo del `exec.Runner`
(binario-no-encontrado/cancelado) en vez de fingir éxito, y `templates.mergeFeatures`
propaga un fallo de `ReadFile` del `pfs.FS` inyectado.

## P3 — Release y operación

### RW-015 — Pipeline GoReleaser — ✅ HECHO
`.goreleaser.yml` sin `go test ./...` en el hook, matriz reducida a Linux
(amd64/arm64), bloque `sboms:`, y workflow `release.yml` por tag que corre los gates
de CI y produce draft release con checksums/SBOM. **Nota:** `goreleaser`/`syft` no
están instalados localmente, así que la config no fue verificada con
`task release -- --snapshot`; se valida en un runner con las herramientas (RW-018).

### RW-016 — Distribuir imagen OCI del CLI — ✅ HECHO

**Decisión tomada (firme):** imagen `ghcr.io/spin-stack/devcontainer-cli`
(source repo `https://github.com/spin-stack/devcontainer-cli`).

**Hecho:**
- `./Dockerfile` — `FROM gcr.io/distroless/static:nonroot` (trae CA certs para el TLS
  a registries), `USER nonroot`, `COPY devcontainer /devcontainer`,
  `ENTRYPOINT ["/devcontainer"]`. Labels OCI `title=devcontainer-cli`,
  `source`, `version`, `revision`, `created`, `licenses`. `VERSION`/`REVISION` por
  `ARG` (los inyecta GoReleaser; en local por `--build-arg`).
- `.goreleaser.yml` — bloques `dockers:` (uno por arch: linux/amd64 y linux/arm64,
  reusando los binarios ya construidos, `use: buildx`) + `docker_manifests:` que
  combinan en `:{{.Version}}` y `:latest`.
- `.github/workflows/release.yml` — job `goreleaser` con `docker/setup-qemu-action`,
  `docker/setup-buildx-action`, login a GHCR (`docker/login-action` con
  `GITHUB_TOKEN`), permisos `packages: write` + `id-token: write`. Tras publicar:
  smoke test `docker run --rm <img> --version` (asserta la versión esperada), registro
  del digest vía `docker buildx imagetools inspect`, y firma keyless + attest SBOM de
  imagen con cosign + syft contra el digest. Gated al path de tag+aprobación; nunca
  desde PRs.

**Provenance/SBOM — decisión técnica:** los flags buildx `--provenance`/`--sbom` NO se
ponen inline en las imágenes por-arch: convierten cada imagen en un OCI index, y el
`docker manifest create` clásico que usa `docker_manifests:` no puede anidarlo
(`"... is a manifest list"`, exit 1 — verificado empíricamente contra un registry
local). En su lugar: SBOM del archive por `sboms:` (syft) + SBOM/firma de imagen por
cosign+syft en el workflow contra el digest inmutable.

**Verificado localmente:** `docker build` + `docker run --rm <img> --version` → `0.0.0-smoke`
(binario estático `CGO_ENABLED=0`, host amd64). Build multi-arch
`docker buildx build --platform linux/amd64,linux/arm64` exitoso (containerd store).
La variante arm64 **no** se puede *ejecutar* localmente (QEMU/binfmt no registrado en
el host); corre nativamente en CI (el smoke test usa la arch del runner). `goreleaser`/
`syft`/`cosign` no están instalados localmente: YAML validado por parseo, `goreleaser
check` y la firma/SBOM de imagen quedan validados en CI (RW-018).

**Aceptación:** `docker run <image> --version` ✅ (local, amd64), amd64/arm64 build ✅,
digest artefactado en el workflow ✅.

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
oráculo y JSON de cada lane guardados, checklist completa. `skipped-arm64` (runtime
arm64 experimental) NO cuenta contra el gate. Bloqueado por RW-012 (alimenta `task
coverage`) y por las colas de RW-005/006.

**Estado de los inconclusive observados** (corrida runtime previa tenía 7): los 2
`*-workspace-secrets` eran un fixture-path faltante — **corregido** (→ matched); los 4
`update-uid-arm64*` eran contención/flakiness bajo ejecución paralela — ahora son
`skipped-arm64` (experimental, opt-in `PARITY_ARM64=true`, matchean aislados con QEMU);
`build.unsupported-platform-failure` es un infra-skip legítimo (fallo a nivel docker).
Así que el gate ya no depende de emulación arm64 en el runner.

## Endurecimiento del harness (wave B — falsos-verdes de la auditoría)

La auditoría de cobertura marcó que una matriz "verde" sobreestima paridad. Estado:

- **Hecho — verificación de digests** (`compare_hashes: true`): el scrub global
  `sha256/hex→<HASH>` ocultaba digests deterministas y comparables; ahora se comparan
  en resolve-dependencies / read-configuration.features-configuration / lockfile.
- **Hecho — null vs absent** (`compare_nulls: true`): `normalizeValue` dropeaba los
  null; ahora los casos-envelope los comparan.
- **Descartado — stderr exacto**: `extractErrorReason` ya canoniza sólo el *formato*
  (tokens que preservan flag/value/choices/arg-name) y compara el *wording* verbatim en
  el fallback (así se cazaron las divergencias de features-test). Un assert de texto
  exacto forzaría a Go a imitar el framing de Node/yargs — contraproducente.
- **Diferido — banner de versión**: Go reporta git-hash y TS semver; el banner es una
  caja cuyo ancho depende del largo de la versión, así que no matchea ni scrubbeando.
  Requiere strippear el banner entero por payoff cosmético (features-test/features-info
  verbose ya pasan por exit_code/stderr).
- **Hecho — cobertura cross-command**: substitución de variables — `${devcontainerId}`
  pinneado por unit test al algoritmo TS (el harness no puede cazarlo: cada lado usa
  id-labels distintos), `${localEnv:X}`/`${localWorkspaceFolderBasename}` en
  `read-configuration.host-variable-substitution`. Merge de metadata-label ya cubierto y
  `match`: `container-metadata-success` (base-image label) + `features-configuration`
  (multi-feature, con `compare_nulls`).
- **Hecho — masking de secrets**: la redacción del logger (`********`, paridad TS
  `maskSecrets`) está unit-testeada (`log.TestSecretMasking`: valor, substring, vacío), y
  los casos `up`/`run-user-commands.workspace-secrets-success` (antes inconclusive por un
  fixture-path faltante, ya corregido) corren con `--log-level trace` + secrets y matchean
  — verificado 0 leaks del valor crudo en la salida de ambos lados.

## Decisiones que deben quedar explícitas

Decisiones ya tomadas (firmes):

- **Plataforma: sólo Linux** (amd64/arm64). Sin Windows ni macOS. → RW-010 cerrado.
- **Runtime: sólo Docker.** Podman no soportado. → RW-009 cerrado.
- **Compose: sólo v2** (`docker compose`). Compose v1 no soportado. → RW-009 cerrado.
- **exec: terminal heredado** (`docker exec -it`), sin PTY propio. → RW-003 Branch A.
- **`--log-file`: tee a archivo** (`io.MultiWriter(os.Stderr, file)`), cableado en los
  comandos que exponen el flag. `--terminal-log-file` teea el mismo stream combinado
  (el CLI tiene un solo stream, sin terminal auto-gestionado): divergencia documentada
  del CLI TS (que escribe dos archivos con formatos distintos). → RW-014 cerrado.
- **Imagen OCI: `ghcr.io/spin-stack/devcontainer-cli`** (source repo
  `github.com/spin-stack/devcontainer-cli`). → RW-016 cerrado.

Puntos que aún no deben permanecer ambiguos:

- fallback de legacy Features por GitHub Releases;
- paridad byte-a-byte del tarball, hoy no alcanzable por `mtime`;
- alcance de ACR/ECR en CI regular o programado (helpers sólo Linux: `secretservice`/`pass`).

Una decisión de no soportar un comportamiento cierra el ítem sólo si se documenta
como divergencia deliberada, se retira la surface engañosa y existe un test del
contrato elegido.
