# Plan de ejecución del trabajo restante

Plan operativo por ítem para [`REMAINING-WORK.md`](REMAINING-WORK.md), con esfuerzo,
dependencias, si requiere doc de diseño, y un **esquema de paralelización con
agentes**. Cada plan parte de la verdad de campo verificada contra el código Go y el
oráculo TS (`reference/` v0.88.0), no de la descripción del backlog. Donde el backlog
estaba desactualizado, se anota la corrección.

> **Estado (al día):** la mayoría de los tracks de este plan ya se ejecutaron y
> viven en `main`. Hecho/cerrado: RW-001, RW-002, RW-003, RW-004, RW-007, RW-009,
> RW-010, RW-011, RW-013, RW-015, RW-017. Parciales: RW-005, RW-006, RW-008, RW-014.
> **Pendientes reales:** RW-012, RW-016, RW-018. El estado autoritativo por ítem vive
> en [`REMAINING-WORK.md`](REMAINING-WORK.md); esta guía se conserva por el grafo de
> dependencias y el esquema de paralelización de abajo.

Convención de esfuerzo: **S** ≤ ½ día · **M** 1–2 días · **L** ≥ 3 días.

---

## Alcance soportado (decisión firme)

- **Sólo Linux.** El CLI Go se soporta y valida **únicamente en Linux** (amd64 y arm64).
  macOS y Windows **no** son objetivos: no se hace runtime, ni E2E, ni release para esas
  plataformas. La lógica cross-platform que exista (simulación `platform="win32"`,
  `consistency=` en workspace mount) se conserva sólo porque el oráculo TS la tiene, pero
  **no** implica soporte.
- **Sin Podman.** El único runtime de contenedores soportado es Docker. Podman **no** se
  soporta. Cualquier detección/lógica de Podman existente es best-effort no garantizado.
- **Sin Compose v1.** Sólo `docker compose` (v2).

Estas decisiones **cierran** RW-009 (Podman/Compose v1) y **reducen** RW-010 (Windows) a
documentación de "no soportado". También acotan RW-008 (sólo credential helpers de Linux)
y RW-015/RW-016 (targets de release y de imagen: sólo `linux/amd64` y `linux/arm64`).

---

## Correcciones al backlog (verificadas)

Antes del plan, cinco supuestos del backlog resultaron inexactos y cambian el trabajo:

- **RW-002:** `features resolve-dependencies` construye los nodos del grafo **sin
  ninguna arista de dependencia** (`features_resolve_deps.go:82-83`): su `installOrder`
  ignora dependencias transitivas. No es un riesgo latente, es un bug ya en producción.
- **RW-004:** el backlog dice revisar `outdated` y `set-up`; ninguno de los dos expone
  ni debe exponer el flag (TS tampoco). El **único** comando que necesita cableado real
  es `build`. `exec`/`read-configuration`/`run-user-commands` registran el flag pero no
  tienen consumidor de compose — y así debe quedar (paridad de superficie con TS).
- **RW-007:** v0.88.0 sólo resuelve OCI image indexes en `inspectImageInRegistry`
  (inspección de imagen base en registry), que **no fue portado a Go**. Features y
  Templates son artefactos de manifiesto único. Los tipos `ImageIndex`/`ImageIndexEntry`/
  `Platform` están **muertos** → la acción correcta es **retirarlos**, no implementar.
- **RW-003:** `lifecycle.ExecWithPTY` y todo `terminal*.go` tienen **cero callers** en
  producción. El path interactivo de Go ya es byte-equivalente al fallback de TS sin
  node-pty (`docker exec -it` con stdio heredado). Recomendación: **borrar el PTY muerto**
  y quedarse con la herencia.
- **RW-008:** dos divergencias reales confirmadas — Go usa `secretservice` donde TS usa
  `secret` (el de Go es el nombre correcto de libsecret; conservarlo), y el cache de auth
  de Go es por-operación mientras TS lo mantiene por-invocación.

Estas correcciones deben reflejarse en `REMAINING-WORK.md` al cerrar cada ítem.

---

## P0 — Paridad funcional

### RW-001 — `overrideFeatureInstallOrder` en `up`/`build` · **S** · doc: no
- **Verdad:** el motor existe y está testeado (`internal/features/order.go`,
  `applyOverridePriority`); el path real lo invoca con `nil`
  (`feature_install.go:480`). Config ya parseada (`config/types.go:44`). Cero efecto hoy.
- **Plan:** propagar `cfg.OverrideFeatureInstallOrder` por `fetchFeatureSets` →
  `extendImageWithFeatures` (preferible un campo `FeatureBuildOptions.OverrideFeatureInstallOrder`,
  menos churn en los 6 call-sites); reemplazar el `nil` de `:480`. Rechazar entradas
  inválidas como TS (`applyOverrideFeatureInstallOrder` lanza; Go hoy las ignora en
  silencio en `order.go:125`).
- **Riesgo:** el *matching* del override en TS es por identidad OCI + `legacyIds`, no por
  string; para paridad discriminante hay que resolver el id igual (`ResolveFeatureID`).
- **Depende de:** RW-002 (idealmente se pliega dentro). Hacer **después** de RW-002.

### RW-002 — Unificar el grafo de Features · **M–L** · doc: sí (nota corta)
- **Verdad:** **tres** grafos independientes (install `feature_install.go:474`,
  resolve-deps JSON `features_resolve_deps.go:52` sin aristas, mermaid `feature_deps.go:21`
  con `roundPriority` fijo en 0). TS tiene **uno** (`buildDependencyGraph`).
- **Plan:** crear `features.BuildDependencyGraph(logger, processFeature, userFeatures,
  overrideOrder, lockfile)` en `internal/features/graph.go`, con `processFeature`
  inyectado (equivalente al closure de TS). El fetch actual del install-path pasa a ser
  la implementación de `processFeature`. `ComputeInstallationOrder` acepta un grafo
  precomputado. Los 3 consumidores (install, resolve-deps, mermaid) usan el mismo builder.
- **Riesgo clave:** preservar lectura de metadata *annotation-first, blob-fallback*; los
  soft-deps NO se expanden recursivamente pero SÍ se les hace `processFeature` para leer
  `legacyIds`; identidad de dedup por recurso+opciones, no por string.
- **Aceptación:** una fixture con `dependsOn` reales → grafo, lockfile e install order
  coherentes; mermaid emite `roundPriority` reales; caso A/B en resolve-dependencies.
- **Habilita:** RW-001 (colapsa a una línea).

### RW-003 — Contrato PTY/señales de `exec` · **S** (Branch A) · doc: sí (ADR corto)
- **Verdad:** `exec` usa `docker exec -it` con stdio heredado (`exec.go:304`). `ExecWithPTY`
  + `terminal*.go` = código muerto (cero callers). El path de Go ya es el fallback
  sin-node-pty de TS → conforme a paridad por definición.
- **Decisión (recomendada, Branch A):** no construir PTY propio; **borrar**
  `internal/lifecycle/pty.go`, `pty_test.go`, `terminal.go`, `terminal_linux.go`,
  `terminal_bsd.go` (y `creack/pty` de go.mod si queda sin uso).
- **Cerrar dos divergencias menores:** (1) endurecer el contrato `128+N` para el caso en
  que el proceso `docker` *host* recibe la señal (hoy `ExitCode()==-1`→255 vs TS 143);
  derivar de `WaitStatus.Signal()` en `exec.go:320`. (2) documentar que
  `--terminal-columns/rows` en modo interactivo siguen al terminal real (limitación
  aceptada). JSON con `-i` es divergencia intencional — **no tocar**.
- **ADR:** "exec = `docker exec -it` heredado; sin PTY propio; contrato 128+N; JSON `-i`
  intencional". Justifica el borrado para que nadie reintroduzca el PTY.
- **Habilita:** RW-010.

### RW-004 — Cablear `--docker-compose-path` · **S** · doc: no
- **Verdad:** sólo `up` (ya OK) y `build` llaman `NewComposeClient`. `build` descarta el
  flag (hardcodea `""` en `build.go:603`). El resto de comandos con el flag no ejecutan
  compose (attach por labels) — igual que TS, que sólo lo threadea donde compose corre.
- **Plan:** en `build`, bindear `dockerComposePath` en `buildOpts` (reemplaza
  `build.go:71`) y pasarlo en `build.go:603`. **No** cablear ni remover nada más. `upgrade`
  mantiene el flag registrado (TS lo tiene) aunque su impl sea lockfile-only.
- **Test:** `build --docker-compose-path /nonexistent/xyz` contra una config compose →
  el error debe mencionar el path custom.

### RW-005 — Promover 2 casos diferidos · **S** · doc: no
- **`build.buildkit-never-platform-failure`:** sin cambio de asserts. El discriminante
  (`--platform or --push require BuildKit enabled.`) coincide byte a byte. Correr con
  Docker+red en host **amd64**; con `matched` en el JSON artefactado, flipear
  `deferred-runtime → match` (`parity-matrix.yaml:597`).
- **`features.test-single-scenario-success`:** cambiar `asserts` a **`[exit_code]`** solo
  (`parity-matrix.yaml:2091`). El stdout de features-test es ANSI verboso y no
  determinista (stream del script del escenario) — no comparable. Correr lane runtime
  serial; con `matched`, flipear a `match`.
- **Alimenta:** RW-018.

---

## P1 — Datos y plataformas

### RW-006 — Interop metadata TS↔Go · **M** · doc: no
- **Verdad:** formato de label array = paridad (`metadata.go:72`). El comentario en
  `metadata.go:66-67` está **desactualizado** (dice "bare object", el código hace array) →
  corregir. TS y Go serializan con whitespace distinto pero **mismo JSON** → comparar
  JSON **parseado**, no bytes del label.
- **Plan:** test bidireccional hermético (registry:3 o `docker build` local): imagen
  construida por TS leída por Go y viceversa; comparar el merge (mounts, ports, users,
  lifecycle, customizations) como JSON normalizado. Reusar fixtures de
  `publish_parity_test.go`.

### RW-007 — OCI image indexes por plataforma · **S** · doc: no (decisión documentada)
- **Verdad:** tipos muertos (grep = 0 referencias). v0.88 sólo los necesitaría para
  `inspectImageInRegistry`, no portado.
- **Plan:** **retirar** `ImageIndex`, `ImageIndexEntry`, `Platform` (`oci/types.go:35-55`)
  y `OCIImageIndexMediaType` (`ref.go:15`). Documentar la decisión: "resolución de índices
  sólo si/cuando se porte `inspectImageInRegistry`; entonces vía oras, no estos structs".

### RW-008 — Registries y credenciales reales · **M** (hermético) / **L** (cloud) · doc: sí (corto)
- **Verdad:** auth vía oras-go; refresh-token/ACR verificado; helper protocol sin test.
  Dos divergencias (ver correcciones): conservar `secretservice`; evaluar hoistear el
  cache de auth al `Client` para reuso entre operaciones relacionadas.
- **Sólo helpers de Linux:** `secretservice` y `pass`. `osxkeychain`/`wincred` quedan
  fuera de alcance (no se soporta macOS/Windows).
- **Plan hermético (ahora):** registry:3 con `htpasswd` → loop 401→bearer→pull/push;
  fake `docker-credential-*` en PATH → protocolo + rama `<token>`→refreshToken; asserts de
  reuso de cache; precedencia con `DOCKER_CONFIG` temporal.
- **Plan cloud (después, gated por secrets):** ACR/ECR/GHCR reales, no bloqueante.
- **Doc:** breve, para fijar la matriz gated-by-secrets, las condiciones de skip y la
  decisión `secretservice`.

### RW-009 — Podman y Compose v1 · **CERRADO (no soportado)** · doc: sí (decisión)
- **Decisión:** Podman y Compose v1 **no se soportan** (ver "Alcance soportado"). Único
  runtime: Docker; único compose: `docker compose` v2.
- **Plan:** no hay implementación. Documentar la divergencia deliberada en
  `GO-REWRITE-STATUS.md` y retirar cualquier superficie que sugiera soporte de Podman
  (flags/mensajes engañosos). Si la detección de Podman existente no cuesta mantener, se
  deja como best-effort **sin** garantía ni test de paridad.

### RW-010 — Paths y ejecución Windows · **CERRADO (no soportado)** · doc: fold en ADR de RW-003
- **Decisión:** Windows **no se soporta** (ver "Alcance soportado"). Sin lane
  `windows-latest`, sin tests de drive-letter/UNC, sin ConPTY.
- **Plan:** documentar "sólo Linux" en README + `GO-REWRITE-STATUS.md`. No se agrega
  código ni CI de Windows. La lógica `platform="win32"` existente se conserva sólo por
  paridad con TS; no se testea como objetivo soportado.

---

## P2 — Calidad orientada a riesgo

### RW-011 — Seams para efectos externos · **M** · doc: sí · **KEYSTONE**
- **Verdad:** hoy existe **un solo seam** (`lifecycle.CommandExecutor`). El resto
  (stdout, proceso, OCI, filesystem) es dependencia concreta inline; por eso los tests
  intercambian `os.Stdout` global (hack racy, no paralelo) — evidencia del propio ítem.
- **Plan:** cuatro interfaces **pequeñas** (no un `CLIHost` monolítico):
  `cli.Output` (Stdout/Stderr, o reusar `cmd.OutOrStdout()`), `oci.Registry` (los 5
  métodos usados; `*oci.Client` ya la satisface), `exec.Runner`, `pfs.FS`. Inyectar por
  comando vía el struct estilo `buildRunner`.
- **Habilita:** RW-012 y RW-014. **Hacer primero** dentro de P2.

### RW-012 — Cobertura de paths críticos · **L** · doc: no
- **Verdad:** gaps mapeados a riesgos nombrados: publish parcial (`push.go:153`),
  cleanup tras fallo (`templates/apply.go`), shell server + env probe
  (`lifecycle/probe.go:23` toma `*ShellServer` concreto), auth/retries OCI.
- **Plan:** (1) extraer `shellExec` interface → testear probe (cache/timeout/PATH-merge) +
  parsers puros **ya**; (2) con fake `oci.Registry`, publish parcial; (3) registry
  httptest para auth + fallo a mitad de loop; (4) `pfs.FS` con WriteFile que falla →
  workspace a medio escribir. Cada incremento cubre **un riesgo nombrado** (no cobertura
  trivial).
- **Depende de:** RW-011 (salvo tests de funciones puras, que empiezan ya).

### RW-013 — Validar inventario de flags · **S–M** · doc: no
- **Verdad:** `cli-flags-inventory.yaml` (1011 líneas) existe como oráculo, sin validar
  contra Cobra. Factible y **independiente**.
- **Plan:** test reflectivo que camina `NewRootCommand()` recursivamente, `VisitAll` sobre
  flags, arma `{name, alias, type, default, hidden}` y diffea contra el YAML en ambas
  direcciones; falla CI ante drift. Reflejar **sólo** campos estructurales — dejar
  `choices`/`implies` (enforcement en `RunE`) a la matriz de paridad.

### RW-014 — Contratos HTTP y host · **M** · doc: no (fold en doc de RW-011)
- **Verdad:** dos stacks HTTP. `httpx.Do` no usa contexto (`httpx.go:70`, sin
  cancelación), sin `CheckRedirect` ni override de transport; `oci` usa
  `retry.DefaultClient` hardcodeado (`orasclient.go:37`), sin punto de inyección.
- **Plan:** `httpx` → `Do(ctx, …)` + `CheckRedirect`/`Transport` opcionales (httptest con
  redirects reales, CA custom, cancelación); `oci` → inyección de transport en `NewClient`
  (reusa la infra httptest de RW-012). Errores de proceso/fs multiplataforma vía los seams
  de RW-011.
- **Depende de:** RW-011 (mitad OCI); la mitad httpx-context es independiente y empieza ya.

---

## P3 — Release y operación

### RW-015 — Pipeline GoReleaser · **M** · doc: no
- **Verdad:** `.goreleaser.yml:6` corre `go test ./...` en el hook `before` → mezcla
  suites Docker/red y `reference/node_modules`. Sin SBOM, sin workflow por tag.
- **Plan:** quitar `go test ./...` del hook (gating = mismos jobs que CI en el workflow de
  tag); **reducir la matriz a sólo Linux**: `goos: [linux]`, `goarch: [amd64, arm64]` (se
  retiran darwin y windows por alcance Linux-only); bloque `sboms:`; workflow
  `release.yml` en `push.tags: ['v*']` → gate + `goreleaser --clean`, draft. Ajustar
  `task build:cross` en consecuencia (sólo linux/amd64 + linux/arm64). Verificar
  `task release -- --snapshot` (requiere instalar goreleaser).

### RW-016 — Imagen OCI del CLI · **M** · doc: light (decisión de nombre) · **decisión abierta**
- **Verdad:** no existe Dockerfile del CLI. Binario estático `CGO_ENABLED=0` → base
  `distroless/static:nonroot` (trae CA certs; el CLI hace TLS a registries).
- **Plan:** `dockers:`/`docker_manifests:` de GoReleaser (reusa binarios) multi-arch
  amd64+arm64; labels OCI `version/source/revision`; provenance/SBOM (`buildx --provenance
  --sbom` o cosign+syft); smoke `docker run <img> --version`; digest artefactado.
- **Decisión abierta:** nombre/registry de la imagen (probable `ghcr.io/devcontainers/cli`).

### RW-017 — Métricas de rendimiento · **S** · doc: no (falta definir aceptación)
- **Verdad:** nada existe; el ítem no tiene criterio de aceptación escrito (el más débil
  del backlog). El oráculo Node está disponible para A/B de startup.
- **Plan:** task `metrics` (hyperfine startup Go vs Node; tamaños binario/comprimido a
  `metrics.json`), job no-bloqueante en release, resultados a `GO-REWRITE-STATUS.md`.
  **Primero:** escribir la definición de aceptación en `REMAINING-WORK.md`.

### RW-018 — Corrida limpia v0.88.0 · **L** · doc: no · **GATE FINAL**
- **Verdad:** secuencia ya especificada (coincide en REMAINING-WORK y RELEASE-CHECKLIST);
  CI ya corre las lanes pero **split**, no como una corrida candidata única. Captura de
  artefactos ya existe (`PARITY_REPORT_FILE`, `reference-commit.txt`, `coverage.out`).
- **Plan:** en el commit candidato, en orden: lint → coverage → integration → e2e →
  reference → parity:contract → parity:network → parity:runtime → build:cross; guardar SHA
  del oráculo + JSON por lane; verificar cero `failed`/`inconclusive` y suma total de
  casos; tildar checklist; recién ahí flipear a "paridad completa".
- **Bloqueado por:** RW-005 (diferidos), RW-001–004 (paridad P0), RW-012 (coverage),
  RW-006/007/008 (lanes runtime/network). RW-015/016/017 **no** lo bloquean (empaquetado).

---

## Grafo de dependencias

```
RW-002 ──▶ RW-001
RW-003 ──▶ RW-010
RW-011 ──▶ RW-012
RW-011 ──▶ RW-014 (mitad OCI)
RW-015 ──▶ RW-016
(RW-001..008, RW-012) ──▶ RW-018 ──▶ release
Independientes: RW-004, RW-005, RW-006, RW-007, RW-009, RW-013, RW-017
```

---

## Esquema de paralelización con agentes

Cada **track** es un agente en su propio *git worktree* (aislamiento contra conflictos de
merge). Los tracks están definidos por **propiedad de archivos disjunta** para poder
correr en paralelo sin pisarse.

### Hotspots de conflicto (a serializar dentro de un track)
- `internal/cli/build.go` — lo tocan RW-004 (compose-path) y RW-011 (helpers de output).
- Cliente OCI / `internal/oci/*` — RW-007, RW-008, y RW-011 (`oci.Registry`).
- `feature_install.go` / `internal/features/*` — RW-001 y RW-002.

### Tracks

| Track | Ítems | Worktree | Notas |
|---|---|---|---|
| **T1 Features graph** | RW-002 → RW-001 | sí | RW-001 se pliega tras RW-002; mismo track (mismos archivos) |
| **T2 Exec (Linux)** | RW-003 (+RW-010 doc) | sí | Borra PTY muerto; RW-010 = cerrar como "no soportado" en docs |
| **T3 Compose+diferidos** | RW-004, RW-005 | sí | mecánicos; RW-005 necesita Docker+red (amd64) |
| **T4 OCI** | RW-007, RW-006, RW-008 | sí | RW-007 primero (borra tipos); luego interop y auth |
| **T5 Seams (keystone)** | RW-011 | sí | **mergear primero**; toca build.go + oci + templates + pfs |
| **T6 Flag inventory** | RW-013 | sí | totalmente independiente (archivo de test nuevo) |
| **T7 Release** | RW-015 → RW-016, RW-017 | sí | disjunto (.goreleaser, workflows, Dockerfile) |

### Olas de ejecución

**Ola 1 (paralelo, ~todos los tracks):** T1(RW-002), T2(RW-003), T3(RW-004+RW-005),
T4(RW-007+RW-006+RW-008), **T5(RW-011)**, T6(RW-013), T7(RW-015).
→ Por el hotspot de `build.go`/OCI, **mergear T5 (RW-011) primero**; T3 y T4 rebasan
encima. Alternativa: que el agente de T5 haga también el cambio de 3 líneas de RW-004.

**Ola 2 (desbloqueada por Ola 1):** RW-001 (tras RW-002, en T1), RW-012 y RW-014 (tras
RW-011), RW-016 y RW-017 (tras RW-015). RW-009 y RW-010 **cerrados por alcance** (sólo
doc, ya en Ola 1).

**Ola 3:** RW-018 — gate final, secuencial, cuando todo lo que afecta paridad está verde.

### Ítems que requieren doc/decisión antes de codear
- **RW-002** — nota de diseño corta (seam `processFeature`, grafo precomputado).
- **RW-003 + RW-010** — un ADR compartido (contrato de exec/PTY/Windows).
- **RW-008** — doc corta de la matriz gated-by-secrets + decisión `secretservice`.
- **RW-009** — decisión de soporte (puede cerrar el ítem como divergencia).
- **RW-011** — doc de las 4 interfaces y la decisión "no CLIHost".
- **RW-016** — decisión de nombre/registry de la imagen.
- **RW-017** — definición de aceptación (métricas concretas).

El resto (RW-001, 004, 005, 006, 007, 012, 013, 014, 015, 018) es mecánico o guiado por
evidencia: sin doc previa.
