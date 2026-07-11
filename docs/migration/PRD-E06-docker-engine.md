# PRD-E06: Motor Docker (single-container path)

**Épica:** E6 — Motor Docker
**Depende de:** E1 (Fundaciones), E2 (Config Spec)
**Desbloquea:** E7 (Compose), E8 (Lifecycle), E9 (Image metadata)
**LoC TS equivalente:** ~1.700
**Prioridad:** P1

---

## 1. Contexto

El path de provisioning single-container es el más usado. Cubre configs basados en `image` o `dockerFile`. Involucra: Docker CLI wrapper, Dockerfile parsing, BuildKit detection, container build/run/inspect, y uid/gid sync.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-node/devContainers.ts` | 278 | Orquestación: launch(), createDockerParams() |
| `src/spec-node/singleContainer.ts` | 454 | Build, extend, spawn container |
| `src/spec-node/dockerfileUtils.ts` | 290 | Parse Dockerfile, extract base image, USER |
| `src/spec-shutdown/dockerUtils.ts` | 428 | Docker CLI wrapper (build, run, inspect, exec) |
| `src/spec-node/utils.ts` | 601 | Retry, inspect, workspace mount, GPU detect |

## 2. Objetivo

Paquetes `internal/docker/` y `internal/provision/single/` que:
1. Wrappeen el Docker CLI para build, run, exec, inspect, tag.
2. Parseen Dockerfiles (extraer stages, FROM, USER, ARG/ENV).
3. Detecten BuildKit availability.
4. Provisionen containers desde image o Dockerfile configs.
5. Manejen workspace mounts, user uid/gid sync, GPU detection.

## 3. Diseño

### 3.1 `internal/docker/client.go` — Docker CLI wrapper

```go
type Client struct {
    DockerPath string
    Host       clihost.CLIHost
    Env        map[string]string
    Log        log.Log
    Platform   PlatformInfo
}

func (c *Client) Build(args ...string) (*ExecResult, error)
func (c *Client) Run(args ...string) (*ExecResult, error)
func (c *Client) Exec(containerID string, args ...string) (*ExecResult, error)
func (c *Client) Inspect(containerID string) (*ContainerInspect, error)
func (c *Client) InspectImage(imageName string) (*ImageInspect, error)
func (c *Client) Tag(source, target string) error
func (c *Client) Stop(containerID string) error
func (c *Client) Rm(containerID string) error
```

**No usa Docker API/SDK** — solo shell-out al binario `docker`, igual que el CLI TS. Esto mantiene compatibilidad con todas las versiones de Docker y Podman.

### 3.2 `internal/docker/dockerfile.go` — Dockerfile parser

**Paridad con:** `src/spec-node/dockerfileUtils.ts` (290 LoC)

```go
type Dockerfile struct {
    Preamble     Preamble
    Stages       []Stage
    StagesByLabel map[string]*Stage
}

type Stage struct {
    From         From
    Instructions []Instruction
}

func ExtractDockerfile(content string) *Dockerfile
func FindBaseImage(df *Dockerfile, buildArgs map[string]string, target string) string
func FindUserStatement(df *Dockerfile, buildArgs, baseImageEnv map[string]string, target string) string
func EnsureDockerfileHasFinalStageName(content string, defaultName string) (stageName string, modified string, err error)
func SupportsBuildContexts(df *Dockerfile) interface{} // bool | "unknown"
```

**Decisión: regex parser vs. moby/buildkit parser.**
- El CLI TS usa regex. Funciona para el subset de Dockerfile que devcontainers necesita.
- `moby/buildkit/frontend/dockerfile/parser` es más robusto pero añade ~5MB al binario.
- **Recomendación:** empezar con regex port; si hay edge cases, evaluar moby parser.

**Variable expression handling en Dockerfile:** `${VAR:-default}`, `${VAR:+alternative}`.

### 3.3 `internal/docker/buildkit.go` — BuildKit detection and buildx flow

```go
func DetectBuildKit(client *Client) (*BuildKitInfo, error)

type BuildKitInfo struct {
    Available bool
    Version   string // e.g., "0.12.0"
}
```

Ejecuta `docker buildx version` y parsea el output.

**Flujo de build con BuildKit** (paridad con `singleContainer.ts:185-211`):

Cuando BuildKit está disponible, el CLI cambia `docker build` por `docker buildx build` y habilita flags adicionales que determinan cómo se construye y distribuye la imagen:

```go
type BuildxOptions struct {
    Platform string // --platform linux/amd64,linux/arm64
    Push     bool   // --push (push directo al registry)
    Output   string // --output type=docker | type=registry | custom
    CacheTo  string // --cache-to type=registry,ref=...
    Labels   []string // --label key=value (repetible)
}

func (c *Client) BuildxBuild(dockerfile string, context string, tags []string, opts BuildxOptions, extraArgs []string) (*ExecResult, error)
```

**Reglas de routing**:
1. Si `--platform` o `--push` → requiere BuildKit. Error si no disponible.
2. Si BuildKit disponible → `docker buildx build` con:
   - `--platform` si especificado.
   - `--push` si especificado (mutuamente exclusivo con `--output`).
   - `--output` si especificado, sino `--load` (default para cargar en docker local).
   - `--cache-to` si especificado.
   - `--build-arg BUILDKIT_INLINE_CACHE=1` siempre.
3. Si BuildKit NO disponible → `docker build` clásico.
   - `--platform`, `--push`, `--output`, `--cache-to` no soportados → error.

**Labels** (`--label`): se pasan como flags repetibles tanto en buildx como en build clásico. Incluyen metadata labels generados por E9 y labels adicionales del usuario (`build --label`).

**Nota**: esta lógica vive en E6, no en E10. E10 solo registra los flags de Cobra y pasa los valores a las funciones de E6. El routing `buildx build` vs `build` y la validación de flag combinations son responsabilidad de este paquete.

### 3.4 `internal/provision/single/provision.go` — Provisioning

```go
func OpenDevContainer(params *ProvisionParams, config *SubstitutedConfig, workspaceConfig *WorkspaceConfig, idLabels []string, additionalFeatures map[string]interface{}) (*ProvisionResult, error)
```

**Flujo para Dockerfile config:**
1. Parse Dockerfile (`ExtractDockerfile`).
2. Extract base image para metadata probing.
3. Ensure final stage name (para build contexts).
4. Build image con `docker build`.
5. *(E9 lo extiende con features)*.
6. Spawn container con `docker run` + mounts + env + user.
7. Inspect container.
8. Return container info.

**Flujo para image config:**
1. Pull/inspect image.
2. *(E9 lo extiende con features)*.
3. Spawn container.

### 3.5 Container state management

```go
func FindExistingContainer(client *Client, idLabels []string) (*ContainerInspect, error)
func FindContainerAndIDLabels(params Params, containerID string, providedLabels []string, workspaceFolder string, configPath string) (*ContainerInspect, []string, error)
```

### 3.6 Workspace mount handling

```go
type WorkspaceMount struct {
    Source      string
    Target      string
    Type        string // "bind" | "volume"
    Consistency string // "cached" | "consistent" | "delegated"
}

func GetWorkspaceMount(host clihost.CLIHost, workspace *Workspace, config *DevContainerConfig, mountGitRoot bool, consistency string) (*WorkspaceMount, error)
```

Incluye lógica de git root detection via `git rev-parse --show-toplevel`.

### 3.7 GPU detection

```go
func CheckDockerGPUSupport(client *Client) (bool, error) // checks nvidia runtime
```

## 4. Criterios de aceptación

- [ ] `docker build` y `docker run` se invocan con los mismos args que el CLI TS para cada fixture.
- [ ] `ExtractDockerfile` pasa todos los tests de `dockerfileUtils.test.ts` (641 LoC).
- [ ] `FindBaseImage` y `FindUserStatement` resuelven variables correctamente.
- [ ] `EnsureDockerfileHasFinalStageName` modifica Dockerfiles sin stage name.
- [ ] BuildKit detection funciona con Docker >= 20.10 y Podman.
- [ ] Workspace mount respeta `--workspace-mount-consistency` y `--mount-workspace-git-root`.
- [ ] Container lookup por id-labels funciona.
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E6.1: Build Docker image from Dockerfile
### US-E6.2: Run container with mounts, env, user
### US-E6.3: Parse Dockerfile and extract base image
### US-E6.4: Detect BuildKit availability
### US-E6.5: Find existing container by labels
### US-E6.6: Inspect container and image
### US-E6.7: Sync remote user UID/GID
### US-E6.8: Detect GPU availability

## 6. Tests a portar

- `src/test/dockerfileUtils.test.ts` (641 LoC) — **crítico**.
- `src/test/dockerUtils.test.ts` (73 LoC).
- `src/test/updateUID.test.ts` (110 LoC).
- `src/test/getEntPasswd.test.ts` (69 LoC).
- `src/test/getHomeFolder.test.ts` (46 LoC).
- Golden tests: `build --workspace-folder` contra fixtures image y dockerfile.

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| Dockerfile regex parser edge cases | Tests exhaustivos de dockerfileUtils. Evaluar moby parser si falla. |
| Podman compatibility | Flag `isPodman` detection. Tests con Podman en CI. |
| Docker CLI output format changes | Parsear JSON output (docker inspect ya es JSON). |
