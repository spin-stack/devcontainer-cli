# PRD-000: Migración de @devcontainers/cli de TypeScript a Go

**Autor:** Manuel de Brito Fontes
**Fecha:** 2026-04-10
**Estado:** Draft
**Versión base TS:** 0.74.0 (commit c8efeb6)

---

## 1. Contexto

El CLI de devcontainers (`@devcontainers/cli`) es la implementación de referencia de la [Dev Containers Specification](https://devcontainers.github.io/). Actualmente es un paquete npm escrito en TypeScript (~16.000 LoC fuente, ~8.500 LoC tests) que requiere Node.js >= 16.13 y una dependencia nativa (`node-pty`) con compilación C++.

La migración a Go busca:
- **Distribución simplificada:** binario estático sin runtime ni dependencias nativas.
- **Rendimiento en CI:** arranque más rápido, sin `npm install`.
- **Portabilidad:** cross-compilation trivial a linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.
- **Mantenibilidad:** reduce la cadena de build (esbuild + tsc + node-pty rebuild) a `go build`.

## 2. Objetivos

1. **Paridad funcional completa** con el CLI TS v0.74.0 para todos los comandos documentados.
2. **Compatibilidad de contrato**: la misma CLI surface (comandos, flags, defaults, JSON output, exit codes) que el CLI TS.
3. **Compatibilidad de datos**: el CLI Go debe leer imágenes etiquetadas por el CLI TS (metadata labels) y viceversa.
4. **Cobertura de tests** >= 80% en cada paquete Go, más golden tests comparativos vs CLI TS.
5. **Distribución**: binarios precompilados en GitHub Releases + imagen OCI `ghcr.io/devcontainers/cli`.

## 3. No-objetivos

- **Cambios al spec**: no se proponen cambios a devcontainers.dev.
- **Nuevos comandos**: `stop` y `down` (marcados como TODO en README) quedan fuera del scope de migración.
- **Reescritura de tests TS**: los tests TS existentes se mantienen y ejecutan contra el CLI TS; el CLI Go usa golden tests + tests propios en Go.
- **Backward compat con Node.js**: el CLI Go no es un paquete npm. Los usuarios que importen `@devcontainers/cli` como librería Node siguen usando el paquete TS.
- **Soporte de plugins/extensiones**: no hay sistema de plugins en el CLI TS; no se añade uno.

## 4. Estrategia de paridad

### 4.1 Contrato de CLI

El archivo `docs/migration/cli-flags-inventory.yaml` es la fuente de verdad de:
- Nombres de comandos y subcomandos
- Nombres de flags, tipos, defaults, aliases, validaciones
- Formato de JSON output envelopes
- Exit codes
- Variables de entorno leídas

**Criterio de aceptación**: el CLI Go pasa `golden-test-harness.sh compare` con 0 fallos sobre todos los fixtures.

### 4.2 Compatibilidad de datos

- **Image metadata labels**: el CLI Go produce y consume el mismo formato de label `devcontainer.metadata` que el CLI TS.
- **Lockfile**: el CLI Go lee y escribe `devcontainer-lock.json` en el mismo formato.
- **Cache folder**: se mantiene la convención `{tmpdir}/devcontainercli[-{username}]`.

### 4.3 Golden tests

El script `docs/migration/golden-test-harness.sh` captura snapshots del CLI TS. Cada snapshot contiene:
- **stdout** normalizado (JSON: keys sorted, campos volátiles eliminados).
- **stderr** normalizado (timestamps, paths, hashes reemplazados por placeholders).
- **exit code**.

**Cobertura actual del harness** (~40 test cases):
- `read-configuration`: 15+ fixtures (image, dockerfile, compose variantes) — sin Docker.
- `build`: 5 fixtures + no-cache variant — con Docker.
- `up`: 6 fixtures (image, dockerfile, compose) — con Docker.
- `set-up`, `run-user-commands`, `exec`: error path tests (container inexistente).
- `outdated`, `upgrade --dry-run`: con features config — sin Docker.
- `features info`, `features info tags`: contra ghcr.io — sin Docker.
- `templates metadata`: contra ghcr.io — sin Docker.

**Limitaciones conocidas** (a expandir conforme avanza la implementación):
- Los tests de error path (set-up, run-user-commands, exec sin container) solo validan el envelope de error, no el flujo happy-path completo.
- `features test/package/publish` y `templates apply/publish` requieren fixtures más elaborados y se añadirán en E4/E5.
- La comparación de stderr es por defecto no-fatal (`GOLDEN_STRICT_STDERR=false`); se endurecerá cuando el formato de logs se estabilice.

Los campos volátiles (containerId, timestamps, paths absolutos, image hashes) se normalizan antes de la comparación.

## 5. Arquitectura Go a alto nivel

### 5.1 Layout de módulos propuesto

```
go.mod                          # github.com/devcontainers/cli
cmd/
  devcontainer/
    main.go                     # Entry point
internal/
  cli/                          # Cobra command wiring (paridad con devContainersSpecCLI.ts)
    root.go
    up.go, build.go, exec.go, ...
    features/                   # features test, package, publish, info, ...
    templates/                  # templates apply, publish, metadata, ...
  config/                       # devcontainer.json schema, loading, merging
    types.go                    # DevContainerConfig unions
    loader.go                   # JSONC parsing, path resolution
    merge.go                    # Config + image metadata merge
    varsub.go                   # Variable substitution
  docker/                       # Docker CLI wrapper
    client.go                   # docker build, run, inspect, exec
    compose.go                  # docker-compose wrapper
    dockerfile.go               # Dockerfile parsing/patching
    pty.go                      # PTY handling (creack/pty)
  features/                     # Feature system
    resolve.go                  # Feature ID routing (OCI, tarball, local, legacy)
    order.go                    # Dependency graph, topological sort
    install.go                  # Dockerfile layer generation, extendImage
    lockfile.go                 # Lockfile read/write
    advisories.go               # Security advisories
  templates/                    # Template system
    fetch.go, apply.go, publish.go
  oci/                          # OCI registry client
    client.go                   # Pull/push manifests and blobs
    auth.go                     # Bearer/Basic/credential-helper auth
    ref.go                      # OCI reference parsing
  lifecycle/                    # Lifecycle hooks
    hooks.go                    # onCreateCommand, postCreateCommand, etc.
    probe.go                    # Remote env probe
    dotfiles.go                 # Dotfiles clone + install
    shell.go                    # Shell server / command serialization
  imagemeta/                    # Image metadata labels
    metadata.go                 # Read/write metadata from Docker labels
    merge.go                    # Merge with config
  core/                         # Cross-cutting
    log.go                      # Logger (text + JSON modes)
    errors.go                   # ContainerError equivalent
    clihost.go                  # CLIHost abstraction (local fs, exec)
    httpx.go                    # HTTP client with proxy support
    jsonc.go                    # JSONC parser wrapper
    pfs.go                      # Filesystem utilities
```

### 5.2 Dependencias Go principales

| Necesidad | Biblioteca | Notas |
|---|---|---|
| CLI framework | `github.com/spf13/cobra` | Paridad de subcomandos, flags, validación |
| JSONC parsing | `github.com/tailscale/hujson` | JSON con comments y trailing commas |
| YAML parsing | `gopkg.in/yaml.v3` | docker-compose.yml |
| Semver | `github.com/Masterminds/semver/v3` | Version resolution |
| OCI client | `oras.land/oras-go/v2` | Pull/push manifests. Reduce ~1500 LoC de OCI propio |
| OCI auth | `github.com/google/go-containerregistry` | Docker credential helpers |
| PTY | `github.com/creack/pty` | Unix PTY. Windows: `github.com/UserExistsError/conpty` |
| Tar | `archive/tar` (stdlib) | |
| HTTP proxy | `golang.org/x/net/http/httpproxy` + stdlib | |
| Terminal color | `github.com/fatih/color` | |
| Table output | `github.com/olekukonko/tablewriter` | |
| URI parsing | Implementación propia (~100 LOC) | Portar lógica de vscode-uri |

### 5.3 Spikes necesarios antes de implementar

1. **JSONC**: validar que `hujson` maneja todos los edge cases de `jsonc-parser` (trailing commas, comentarios en arrays, etc.) contra los fixtures de devcontainer.json.
2. **PTY multiplataforma**: validar `creack/pty` en darwin+linux y `conpty` en Windows para el comando `exec`.
3. **Dockerfile parsing**: evaluar si `moby/buildkit/frontend/dockerfile/parser` es viable como dependencia vs. portar el parser regex de `dockerfileUtils.ts`.
4. **OCI client**: validar que `oras-go` cubre el flujo de auth (Bearer + credential helpers) con ghcr.io, ACR, ECR.

## 6. Roadmap de épicas

Las épicas están ordenadas por dependencias técnicas. E1-E3 son fundacionales; E4-E9 son paralelizables en parte; E10 es integración final.

| # | Épica | LoC TS equiv. | Dependencias | Prioridad |
|---|---|---|---|---|
| E1 | Fundaciones (log, errors, clihost, httpx, jsonc, pfs) | ~800 | ninguna | P0 |
| E2 | Config spec (tipos, carga, merge, varsub) | ~600 | E1 | P0 |
| E3 | OCI registry client | ~1500 | E1 | P0 |
| E4 | Features (parsing, orden, lockfile, advisories) | ~2200 | E1, E3 | P1 |
| E5 | Templates | ~500 | E3 | P1 |
| E6 | Motor Docker (single-container) | ~1700 | E1, E2 | P1 |
| E7 | Motor Docker Compose | ~900 | E6 | P2 |
| E8 | Lifecycle + exec + PTY | ~1400 | E6, E9 | P2 |
| E9 | Image metadata + extendImage | ~1100 | E4, E6 | P2 |
| E10 | CLI wiring (Cobra) + golden tests | — | todas | P3 |

### 6.1 Diagrama de dependencias

```
E1 (Fundaciones)
├── E2 (Config) ──────────┐
│   └── E6 (Docker) ──────┤
│       ├── E7 (Compose)   │
│       └── E9 (Metadata) ─┤
│           └── E8 (Life.) ─┤──── E10 (CLI wiring)
└── E3 (OCI) ─────────────┤
    ├── E4 (Features) ──→E9┘
    └── E5 (Templates)
```

**Cambio vs. versión anterior**: E8 ahora depende de E9 (consume `MergedDevContainerConfig`). Esto implica que E9 debe completarse antes o en paralelo con E8, definiendo al menos la interfaz de `MergedConfig` tempranamente.

## 7. Estrategia de release y cohabitación

### Fase 1: Desarrollo paralelo
- El CLI TS sigue siendo el oficial (`@devcontainers/cli` en npm).
- El CLI Go se desarrolla en un repositorio o branch separado.
- Golden tests corren en CI contra ambos.

### Fase 2: Beta pública
- Publicar binarios Go en GitHub Releases como `devcontainer-go` (nombre temporal).
- Documentar diferencias conocidas.
- Invitar a la comunidad a probar.

### Fase 3: Transición
- Cuando golden tests pasan al 100% y no hay regresiones reportadas, el CLI Go se convierte en `devcontainer`.
- El paquete npm se mantiene pero marca deprecation.
- VS Code y Codespaces migran al binario Go.

## 8. Estrategia de validación

### 8.1 Tests unitarios (Go)
- Cada paquete Go tiene tests propios (tabla-driven, fixtures inline).
- Objetivo: >= 80% coverage por paquete.

### 8.2 Golden tests (cross-CLI)
- Script `golden-test-harness.sh` captura snapshots del CLI TS.
- CI ejecuta `compare` contra CLI Go en cada PR.
- Categorías: read-configuration (sin Docker), build (con Docker), up (con Docker).

### 8.3 Tests de integración (Go)
- Tests que arrancan Docker containers reales, similar a `src/test/cli.up.test.ts`.
- Usan los mismos fixtures de `src/test/configs/`.
- Corren en CI con Docker disponible.

### 8.4 Tests de regresión OCI
- Tests contra registries reales (ghcr.io, ACR) con credenciales en CI secrets.
- Equivalente a `registryCompatibilityOCI.test.ts`.

## 9. Riesgos y mitigaciones

| Riesgo | Impacto | Probabilidad | Mitigación |
|---|---|---|---|
| node-pty no tiene equivalente exacto en Go/Windows | `exec` no funciona en Windows | Media | Spike E8 con `conpty`. Aceptar limitaciones documentadas. |
| JSONC edge cases no cubiertos por `hujson` | Config parsing falla | Baja | Spike E1. Tener parser JSONC fallback propio. |
| Dockerfile regex parser no cubre todos los edge cases | Feature injection falla | Media | Evaluar `moby/buildkit` parser. Comparar contra todos los fixtures. |
| OCI auth con ECR/ACR difiere del flujo Docker | Publish falla en registries enterprise | Media | Test matrix con registries reales en CI. |
| Backwards compat de metadata labels entre CLI TS y Go | Imágenes viejas no se leen correctamente | Alta (si se rompe) | Golden tests incluyen `getImageMetadataFromContainer` paths. |
| Docker Compose YAML edge cases | Compose provisioning falla | Baja | `go-yaml` es maduro. Tests contra mismos fixtures. |

## 10. Métricas de éxito

1. **100% de golden tests pasando** sobre todos los fixtures disponibles.
2. **0 regresiones** reportadas durante Fase 2 (beta) en el issue tracker.
3. **Tiempo de arranque** del CLI Go <= 50ms (vs ~300ms del CLI TS con Node.js).
4. **Tamaño de binario** <= 30MB (vs ~200MB instalación npm con node_modules).
5. **CI build time** reducido en >= 50% para consumidores que usan el CLI en pipelines.

## 11. Documentos relacionados

### Artefactos de planificación
- `docs/migration/cli-flags-inventory.yaml` — Inventario completo de flags (contrato de paridad)
- `docs/migration/golden-test-harness.sh` — Script de golden tests

### PRDs por épica
- `docs/migration/PRD-E01-foundations.md` — E1: Fundaciones (log, errors, clihost, httpx, jsonc)
- `docs/migration/PRD-E02-config-spec.md` — E2: Config Spec (tipos, carga, merge, varsub)
- `docs/migration/PRD-E03-oci-client.md` — E3: OCI Registry Client (pull, push, auth)
- `docs/migration/PRD-E04-features.md` — E4: Features (parsing, orden, lockfile, advisories)
- `docs/migration/PRD-E05-templates.md` — E5: Templates (apply, publish, metadata)
- `docs/migration/PRD-E06-docker-engine.md` — E6: Motor Docker single-container
- `docs/migration/PRD-E07-docker-compose.md` — E7: Motor Docker Compose
- `docs/migration/PRD-E08-lifecycle-pty.md` — E8: Lifecycle hooks, exec, PTY, dotfiles
- `docs/migration/PRD-E09-image-metadata.md` — E9: Image metadata + extendImage
- `docs/migration/PRD-E10-cli-wiring.md` — E10: CLI wiring (Cobra) + golden tests + release

### Referencias externas
- [devcontainers.dev spec](https://devcontainers.github.io/) — Especificación oficial
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec) — Protocolo de registro
