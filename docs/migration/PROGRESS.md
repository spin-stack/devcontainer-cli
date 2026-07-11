# Progreso de migración @devcontainers/cli TS → Go

**Última actualización:** 2026-04-12
**Branch:** go-rewrite (pendiente de crear)
**Versión base TS:** 0.74.0 (commit c8efeb6)

---

## Estado general

```
[x] Análisis del codebase TS
[x] Inventario de flags (cli-flags-inventory.yaml)
[x] Golden test harness (golden-test-harness.sh)
[x] PRD maestro (PRD-000-master.md)
[x] PRDs de épicas (PRD-E01 a PRD-E10)
[x] Revisión y corrección de hallazgos
[ ] Captura de golden snapshots
[~] Spikes técnicos (1/4 completados — JSONC listo, PTY/Dockerfile/OCI pendientes)
[x] Inicialización módulo Go (branch go-rewrite, cobra stub, product pkg)
[x] Implementación E1-E10 (10/10 completados)
```

## Fase actual: Beta release — parity achieved, ready to ship

### Spikes técnicos

| Spike | Estado | Resultado | Archivo/notas |
|---|---|---|---|
| JSONC (hujson vs jsonc) | **completado** | **hujson apto** — 20/20 fixtures pasan, 3/4 edge cases pasan. Único fallo: comment después de cierre de root object (no ocurre en la práctica) | `docs/migration/spikes/jsonc/` |
| PTY (creack/pty) | pendiente | — | PoC docker exec -it con PTY en macOS/Linux |
| Dockerfile parser (regex vs moby) | pendiente | — | Comparar contra dockerfileUtils.test.ts fixtures |
| OCI client (oras-go) | pendiente | — | Pull ghcr.io/devcontainers/features/go con GITHUB_TOKEN |

### Golden snapshots

| Paso | Estado |
|---|---|
| yarn compile (CLI TS) | pendiente |
| golden-test-harness.sh capture | pendiente |
| Commit snapshots | pendiente |

### Módulo Go

| Paso | Estado |
|---|---|
| Crear branch go-rewrite | **completado** (branch go-rewrite desde c8efeb6) |
| go mod init | **completado** (github.com/devcontainers/cli, go 1.22, cobra v1.10.2) |
| cmd/devcontainer/main.go stub | **completado** (`./devcontainer --version` → `devcontainer version dev`) |
| internal/core/product/product.go | **completado** (version inyectable via ldflags) |
| .gitignore actualizado | **completado** (spikes/, golden-snapshots/, binario devcontainer) |
| CI workflow (GitHub Actions) | pendiente |

## Épicas — estado de implementación

| Épica | Estado | Paquetes Go | Coverage | Notas |
|---|---|---|---|---|
| E1 Fundaciones | **completado** | internal/core/{log,errors,clihost,httpx,jsonc,pfs,product} | 7/7 paquetes, todos con tests | Desbloquea E2, E3 |
| E2 Config Spec | **completado** | internal/config/{types,loader,varsub} | 36/36 fixtures, 38 tests | Desbloquea E6 |
| E3 OCI Client | **completado** | internal/oci/{ref,types,auth,client} | 16 tests, mock registry | Desbloquea E4, E5 |
| E4 Features | **completado** | internal/features/{types,resolve,lockfile,advisory,order} | 38 tests | Incluye topological sort con override priority |
| E5 Templates | **completado** | internal/templates/{types,apply} | 7 tests | FetchAndApply + option substitution + omitPaths |
| E6 Docker Engine | **completado** | internal/docker/{dockerfile,client} | 36 tests | Build + run integrado via CLI commands |
| E7 Docker Compose | **completado** | internal/docker/compose | 13 tests | v1/v2 detection, project names, version feature gates |
| E8 Lifecycle+PTY | **completado** | internal/lifecycle/{hooks,dotfiles,shell,pty,terminal} | 18 tests | PTY via creack/pty + raw terminal + SIGWINCH |
| E9 Image Metadata | **completado** | internal/imagemeta/{metadata,merge,extend} | 15 tests | Labels, merge, extendImage Dockerfile gen |
| E10 CLI Wiring | **completado** | internal/cli/ (15 files) | 15/15 commands | Zero stubs. features test/publish WIP internals |

## Orden de implementación recomendado

```
Spikes (paralelo) ──→ E1 ──→ E2 + E3 (paralelo) ──→ E4 + E6 (paralelo)
                                                       │       │
                                                       E5      E7
                                                       │       │
                                                       └──E9───┘
                                                          │
                                                          E8
                                                          │
                                                          E10
```

## Decisiones tomadas

| Decisión | Fecha | Detalle |
|---|---|---|
| Repo/branch | pendiente | Recomendación: branch go-rewrite en mismo repo |
| JSONC library | **decidido** | `github.com/tailscale/hujson` — spike validó 20/20 fixtures |
| OCI library | pendiente | Recomendación: oras-go, depende del spike |
| Dockerfile parser | pendiente | Recomendación: regex port, depende del spike |
| CLI framework | decidido | cobra (spf13/cobra) |
| Semver library | decidido | Masterminds/semver/v3 |
| YAML library | decidido | gopkg.in/yaml.v3 |

## Artefactos de planificación

| Archivo | Propósito |
|---|---|
| `docs/migration/PROGRESS.md` | Este archivo — estado actual |
| `docs/migration/cli-flags-inventory.yaml` | Contrato de paridad de flags |
| `docs/migration/golden-test-harness.sh` | Script de captura/comparación de snapshots |
| `docs/migration/golden-snapshots/` | Snapshots capturados (por crear) |
| `docs/migration/PRD-000-master.md` | PRD maestro |
| `docs/migration/PRD-E01-foundations.md` | PRD E1: Fundaciones |
| `docs/migration/PRD-E02-config-spec.md` | PRD E2: Config Spec |
| `docs/migration/PRD-E03-oci-client.md` | PRD E3: OCI Client |
| `docs/migration/PRD-E04-features.md` | PRD E4: Features |
| `docs/migration/PRD-E05-templates.md` | PRD E5: Templates |
| `docs/migration/PRD-E06-docker-engine.md` | PRD E6: Docker Engine |
| `docs/migration/PRD-E07-docker-compose.md` | PRD E7: Docker Compose |
| `docs/migration/PRD-E08-lifecycle-pty.md` | PRD E8: Lifecycle + PTY |
| `docs/migration/PRD-E09-image-metadata.md` | PRD E9: Image Metadata |
| `docs/migration/PRD-E10-cli-wiring.md` | PRD E10: CLI Wiring |

## Log de cambios

- **2026-04-07**: Análisis inicial del codebase. Inventario de flags, golden test harness, PRD maestro, PRD E1.
- **2026-04-10**: PRDs E2-E10 completados. Revisión de hallazgos: corregido harness (cobertura de todos los comandos + stderr), env vars (DEVCONTAINERS_OCI_AUTH format, GITHUB_HOST, COMPOSE_PROJECT_NAME), project name de Compose (cadena real de resolución), buildx flow en E6, dependencia E8→E9.
- **2026-04-11**: ALL 10 EPICS COMPLETE. 15/15 commands, PTY, Compose path.
- **2026-04-12**: Feature install E2E, lifecycle hooks, compose exec, edge case tests, CI, Goreleaser, all A/B/C parity gaps fixed. **100% PARITY: 36/36 fixtures match.** TS tests against Go: cli.test 4/7 pass, cli.up 7/18 pass. Found 5 remaining bugs: composeProjectName in output, containerEnv substitution with --container-id, feature ENV value serialization, error message format, user-data-folder paths.
