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

## P0 — Paridad funcional

### RW-001 — Aplicar `overrideFeatureInstallOrder` en `up` y `build`

**Estado actual:** `features.ComputeInstallationOrder` soporta prioridades, pero el
path real de instalación lo invoca con `nil`. `resolve-dependencies` y la
instalación no consumen exactamente la misma configuración.

**Trabajo:**

- transportar `config.overrideFeatureInstallOrder` hasta `fetchFeatureSets`;
- aplicar prioridades después de expandir `dependsOn`;
- verificar interacción con dependencias transitivas, aliases y features locales;
- rechazar entradas del override que TS rechaza.

**Aceptación:** test hermético de orden discriminante y caso A/B en `build` o `up`.

### RW-002 — Unificar el grafo de Features

**Estado actual:** instalación y `features resolve-dependencies` construyen grafos
separados (`feature_install.go` y `feature_deps.go`). Esto permite divergencias en
deduplicación, aliases, opciones y ciclos.

**Trabajo:**

- crear un resolver compartido con nodos, edges, source identity y metadata;
- usarlo para instalación, lockfile, install order y render Mermaid;
- deduplicar por identidad OCI/digest/aliases, no sólo por string de entrada;
- conservar opciones de la primera feature equivalente como lo hace TS.

**Aceptación:** una misma fixture produce grafo, lockfile e instalación coherentes;
tests de diamond dependency, tags equivalentes, legacy alias y ciclo.

### RW-003 — Resolver el contrato PTY/señales de `exec`

**Estado actual:** `ExecWithPTY` existe y tiene tests aislados, pero cero callers.
`exec` hereda el terminal de `docker exec -it`; `terminal-columns/rows` sólo llegan
al logger y no hay forwarding explícito de señales ni contrato `128+signal`.

**Trabajo:**

- decidir y documentar si el PTY propio es necesario para paridad v0.88.0;
- si lo es, cablearlo en Unix, aplicar dimensiones iniciales y `SIGWINCH`;
- propagar señales y exit code `128+N`;
- definir Windows: ConPTY, fallback no interactivo o limitación soportada;
- eliminar código PTY muerto si se demuestra que la herencia directa es equivalente.

**Aceptación:** E2E interactivo Unix, E2E piped, resize, señal y comportamiento
Windows documentado y probado en CI.

### RW-004 — Cablear completamente `--docker-compose-path`

**Estado actual:** algunos comandos registran el flag pero no lo almacenan o no lo
entregan a `NewComposeClient`; `up` sí tiene un path más completo.

**Trabajo:** revisar `build`, `read-configuration`, `exec`,
`run-user-commands`, `outdated` y `set-up`; eliminar flags que el TS no usa o
cablearlos de extremo a extremo.

**Aceptación:** tests con un wrapper Compose discriminante para cada comando que
expone el flag.

### RW-005 — Cerrar casos diferidos de la matriz

Casos actuales:

- `build.buildkit-never-platform-failure`;
- `features.test-single-scenario-success`.

**Aceptación:** ejecución efectiva con Docker/red, `matched` observado y cambio de
`current_status` basado en el JSON artefactado.

## P1 — Compatibilidad de datos y plataformas

### RW-006 — Interoperabilidad explícita de metadata TS ↔ Go

**Trabajo:** construir una imagen con TS y leerla con Go; construir con Go y leerla
con TS. Comparar label array, merge, lifecycle, customizations, mounts, ports y
usuarios.

**Aceptación:** E2E bidireccional contra la misma imagen exportada o registry local.

### RW-007 — Resolver OCI image indexes por plataforma

**Estado actual:** existen los tipos `ImageIndex`/`ImageIndexEntry`, pero no una
operación de selección por OS/arquitectura/variant.

**Trabajo:** confirmar si v0.88.0 aún requiere esta operación en Features/Templates;
implementar selección y digest verification o retirar los tipos muertos con una
decisión documentada.

**Aceptación:** fixture OCI multi-platform local con amd64, arm64 y arm64/v8.

### RW-008 — Validar registries y credenciales reales

**Trabajo:**

- ACR con identity/refresh token;
- ECR con helper/login estándar;
- GHCR autenticado;
- helpers `pass`/`secretservice`, `osxkeychain` y `wincred` por plataforma;
- comprobar reutilización de auth cache entre operaciones relacionadas.

**Aceptación:** matriz de integración protegida por secrets, sin imprimir secretos,
con pull, tags y push.

### RW-009 — Podman y Compose v1 — CERRADO: no soportado

**Decisión (firme):** el CLI Go soporta **sólo Docker** y **sólo `docker compose` v2**.
Podman y Compose v1 **no se soportan** — divergencia deliberada. Ninguna lógica de
detección de Podman ofrece garantía ni test de paridad; es best-effort no soportado.

**Trabajo restante:** sólo documentación (nota en `GO-REWRITE-STATUS.md`) y retirar
cualquier flag/mensaje que insinúe soporte de Podman.

### RW-010 — Paths y ejecución Windows — CERRADO: no soportado

**Decisión (firme):** el CLI Go se soporta **sólo en Linux** (amd64/arm64). Windows y
macOS **no** son objetivos: sin runtime, sin E2E, sin release, sin lane `windows-latest`,
sin ConPTY. La lógica `platform="win32"` se conserva sólo por paridad con el oráculo TS.

**Trabajo restante:** sólo documentación ("sólo Linux" en README + `GO-REWRITE-STATUS.md`).

## P2 — Calidad orientada a riesgo

### RW-011 — Introducir seams para efectos externos

**Motivo:** CLI y Templates dependen directamente de `os.Stdout`, `os/exec`, Docker
y un `*oci.Client` concreto, lo que impide probar errores parciales herméticamente.

**Trabajo:** interfaces pequeñas para process runner, OCI operations, filesystem,
clock y output; evitar una abstracción `CLIHost` monolítica salvo que aporte valor.

**Aceptación:** tests sin Docker para propagación de errores, cancelación y cleanup
en `build/up/exec/templates apply/publish`.

### RW-012 — Cobertura de paths críticos

Baseline unitario aproximado:

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

No se exige subir números mediante tests triviales. Prioridad:

- errores entre capas y cancelación;
- cleanup tras fallos parciales;
- publish parcial;
- auth/retries OCI;
- shell server y user env probe real;
- templates con workspace parcialmente escrito;
- Docker/Compose argument construction.

**Aceptación:** cada incremento cubre un riesgo nombrado. El objetivo de referencia de
80% por paquete se mantiene como dirección, no como sustituto de paridad E2E.

### RW-013 — Validar inventario de flags automáticamente

**Trabajo:** comparar `cli-flags-inventory.yaml` con el árbol Cobra para comandos,
flags, aliases, tipos, defaults, hidden y validaciones; fallar CI ante drift.

**Aceptación:** test generado/reflectivo sin listas duplicadas escritas a mano.

### RW-014 — Completar contratos de HTTP y host

**Trabajo:** integración de proxy real, redirects, CA custom, cancelación y errores
de proceso/filesystem multiplataforma. Evaluar interfaces pequeñas en RW-011 en vez
de una abstracción `CLIHost` monolítica.

## P3 — Release y operación

### RW-015 — Corregir pipeline GoReleaser

**Estado actual:** `.goreleaser.yml` ejecuta `go test ./...`, lo que vuelve a mezclar
unit, paridad y paquetes accidentales bajo `reference/node_modules`.

**Trabajo:** usar los mismos gates que CI, validar cinco targets y agregar un workflow
por tag que genere draft release con checksums/SBOM.

**Aceptación:** `task release -- --snapshot` pasa en limpio y el workflow por tag
produce artefactos sin publicar hasta aprobación.

### RW-016 — Distribuir imagen OCI del CLI

**Trabajo:** imagen mínima multi-arch con el binario estático, provenance/SBOM,
version label y smoke test; publicar bajo el nombre acordado.

**Aceptación:** `docker run <image> --version`, amd64/arm64, digest artefactado.

### RW-017 — Métricas de rendimiento y distribución

Medir y registrar por release:

- startup del comando local;
- tamaño de binarios y archivos comprimidos;
- comparación de tiempo/costo frente al CLI Node;
- límites o regresiones aceptados.

### RW-018 — Corrida limpia de paridad v0.88.0

Ejecutar en el commit candidato:

```sh
task lint
task coverage
task test:integration
task test:e2e
task parity:contract
task parity:network
task parity:runtime
task build:cross
```

**Aceptación:** cero `failed`, cero `inconclusive`, deferred resueltos, SHA del
oráculo y JSON de cada lane guardados, checklist completa.

## Decisiones que deben quedar explícitas

Decisiones ya tomadas (firmes):

- **Plataforma: sólo Linux** (amd64/arm64). Sin Windows ni macOS. → RW-010 cerrado.
- **Runtime: sólo Docker.** Podman no soportado. → RW-009 cerrado.
- **Compose: sólo v2** (`docker compose`). Compose v1 no soportado. → RW-009 cerrado.
- **exec: terminal heredado** (`docker exec -it`), sin PTY propio. → RW-003 Branch A.

Puntos que aún no deben permanecer ambiguos:

- fallback de legacy Features por GitHub Releases;
- paridad byte-a-byte del tarball, que hoy se considera no alcanzable por `mtime`;
- alcance de ACR/ECR en CI regular o programado (helpers sólo Linux: `secretservice`/`pass`).

Una decisión de no soportar un comportamiento cierra el ítem sólo si se documenta
como divergencia deliberada, se retira la surface engañosa y existe un test del
contrato elegido.
