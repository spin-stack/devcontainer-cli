# PRD-E07: Motor Docker Compose

**Épica:** E7 — Docker Compose
**Depende de:** E6 (Docker single-container)
**Desbloquea:** E10 (CLI wiring)
**LoC TS equivalente:** ~900
**Prioridad:** P2

---

## 1. Contexto

Cuando `devcontainer.json` usa `dockerComposeFile`, el CLI orquesta multi-service setups via Docker Compose. Esto incluye parsing de docker-compose.yml, generación de override files, build del servicio target, y extensión con features.

**Archivo TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-node/dockerCompose.ts` | 764 | Todo el path de Compose |

## 2. Objetivo

Un paquete `internal/provision/compose/` que:
1. Lea y parsee docker-compose.yml (con `go-yaml`).
2. Genere override files para build args, contexts y features.
3. Ejecute `docker compose` (v2) o `docker-compose` (v1) commands.
4. Resuelva project name (explícito, workspace basename, o hash).
5. Extraiga la imagen del servicio target para metadata probing.

## 3. Diseño

### 3.1 Compose CLI wrapper

```go
type ComposeClient struct {
    CLIPath   string // "docker-compose" or "docker compose"
    IsV2      bool
    DockerCLI *docker.Client
    Log       log.Log
    Env       map[string]string
}

func NewComposeClient(host clihost.CLIHost, dockerPath string, composePath string) (*ComposeClient, error)
func (c *ComposeClient) Config(files []string, envFile string) (*ComposeConfig, error)
func (c *ComposeClient) Build(files []string, globalArgs []string, services []string) error
func (c *ComposeClient) Up(files []string, globalArgs []string, services []string) error
func (c *ComposeClient) Down(files []string, globalArgs []string) error
```

### 3.2 Compose config parsing

```go
type ComposeConfig struct {
    Version  string                    `yaml:"version,omitempty"`
    Name     string                    `yaml:"name,omitempty"`
    Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
    Image   string      `yaml:"image,omitempty"`
    Build   interface{} `yaml:"build,omitempty"` // string | object
    // ...otros campos relevantes
}
```

**Nota:** Solo parseamos los campos que el CLI necesita; no implementamos un parser completo de compose-spec.

### 3.3 Project name resolution

**Paridad con:** `src/spec-node/dockerCompose.ts:638-681` (`getProjectName`)

```go
func GetProjectName(params *Params, workspace *Workspace, composeFiles []string, composeConfig *ComposeConfig) (string, error)
```

Cadena de resolución (en este orden exacto, paridad con el TS):

1. **`COMPOSE_PROJECT_NAME` env var** — si está definido en el entorno del proceso, gana. Se sanitiza con `toProjectName()`.
2. **`COMPOSE_PROJECT_NAME` en `.env`** — si existe un archivo `.env` en cwd con `COMPOSE_PROJECT_NAME=value`, se usa. Se sanitiza.
3. **`name` field en compose YAML** — si `composeConfig.name` está presente:
   - Si el valor es distinto de `"devcontainer"`, se usa directamente.
   - Si el valor es `"devcontainer"`, se verifica si algún compose file contiene explícitamente `name:` (podría ser el default de `docker compose config`). Solo se usa si es explícito en un compose file.
4. **Derivación desde directorio** — fallback final:
   - Si el working dir del primer compose file es `{configFolderPath}/.devcontainer`, se usa `{basename(configFolderPath)}_devcontainer`.
   - En cualquier otro caso, se usa `basename(workingDir)` donde `workingDir` es `dirname(composeFiles[0])`.

**Sanitización** (`toProjectName`):
- Compose < 1.21.0: solo `[a-z0-9]` (lowercase, elimina todo lo demás).
- Compose >= 1.21.0: `[a-z0-9_-]` (permite guiones y underscores).
- La versión de Compose se detecta con `docker-compose --version`.

**Nota**: el handling especial de `"devcontainer"` como valor de `name` es para distinguir entre el usuario escribiendo `name: devcontainer` explícitamente y `docker compose config` emitiendo `name: devcontainer` como default derivado del directorio.

### 3.4 Override file generation

```go
func GenerateOverrideFile(params *Params, service string, buildTarget string, additionalContexts map[string]string, cacheFrom []string) (string, error)
```

Genera un archivo temporal `docker-compose.devcontainer.build.yml` con:
- `build.target` override (para feature injection stage).
- `build.args` adicionales.
- `build.additional_contexts` (para BuildKit >= compose 2.17).
- `build.cache_from` adicional.

### 3.5 Provisioning flow

```go
func OpenDockerComposeDevContainer(params *ProvisionParams, workspace *Workspace, config *SubstitutedConfig, idLabels []string, additionalFeatures map[string]interface{}) (*ProvisionResult, error)
```

1. Resolve compose files (E2 `GetDockerComposeFilePaths`).
2. Read compose config (`docker compose config`).
3. Validate target service exists.
4. Get project name.
5. Build service image (with override file).
6. *(E9 extends with features)*.
7. `docker compose up` for target service + runServices.
8. Inspect running container.

### 3.6 Version prefix detection

```go
func ReadVersionPrefix(host clihost.CLIHost, composeFiles []string) (string, error)
```

Detecta `version: "3"` o `version: "2"` en compose files para compatibility behavior.

## 4. Criterios de aceptación

- [ ] Todos los fixtures `compose-*` de `src/test/configs/` se provisionen correctamente.
- [ ] Override file generation produce YAML válido.
- [ ] Project name resolution sigue la cadena de fallback.
- [ ] Compose v1 y v2 CLI se detectan y usan correctamente.
- [ ] Service image extraction funciona para `image:` y `build:` (string y object forms).
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E7.1: Parse docker-compose.yml and extract service config
### US-E7.2: Resolve project name from config/env/workspace
### US-E7.3: Generate build override file for feature injection
### US-E7.4: Build and start compose service
### US-E7.5: Detect Compose CLI version (v1 vs v2)

## 6. Tests a portar

- `src/test/dockerComposeUtils.test.ts` (99 LoC).
- Golden tests: `up --workspace-folder` y `build --workspace-folder` contra fixtures `compose-*`.
- `src/test/cli.up.test.ts` compose-related test cases.

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| docker-compose v1 en deprecation | Detectar y warn; priorizar v2 |
| YAML parsing edge cases | `go-yaml/v3` es robusto; tests contra todos los fixtures |
| Override file merging complejidad | Generar YAML limpio, no merge manual |
