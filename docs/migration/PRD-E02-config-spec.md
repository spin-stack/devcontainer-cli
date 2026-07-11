# PRD-E02: Config Spec — Tipos, carga, merge y variable substitution

**Épica:** E2 — Config Spec
**Depende de:** E1 (Fundaciones)
**Desbloquea:** E6 (Docker single-container), E7 (Docker Compose)
**LoC TS equivalente:** ~600
**Prioridad:** P0

---

## 1. Contexto

El corazón del CLI es la carga, validación y transformación de `devcontainer.json`. Este archivo usa JSONC (JSON con comments), soporta 3 variantes de configuración (image, Dockerfile, docker-compose) y contiene variables `${...}` que se resuelven en fases.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-configuration/configuration.ts` | 269 | Tipos DevContainerConfig, path resolution, compose file discovery |
| `src/spec-common/variableSubstitution.ts` | 170 | `${localEnv:X}`, `${containerEnv:X}`, `${devcontainerId}`, etc. |
| `src/spec-configuration/configurationCommonUtils.ts` | 75 | URI↔fs path, well-known config paths |
| `src/spec-node/configContainer.ts` | 117 | readDevContainerConfigFile, resolve routing |
| `src/spec-utils/workspaces.ts` | 35 | Workspace abstraction |
| `src/spec-configuration/editableFiles.ts` | 163 | Remote document abstraction (URI schemes) |

## 2. Objetivo

Paquetes Go que permitan:
1. Cargar un `devcontainer.json` desde disco (JSONC → struct tipado).
2. Determinar la variante de config (image, Dockerfile, Compose).
3. Aplicar variable substitution en fases (host-side y container-side).
4. Resolver paths de Dockerfile y docker-compose relativos al config.
5. Producir un `SubstitutedConfig` listo para consumo por los motores de provisioning.

## 3. No-objetivos

- Image metadata merging (E9).
- Feature resolution (E4).
- Docker/Compose execution (E6/E7).
- Override config merging (implementación simple aquí, merge profundo en E9).

## 4. Diseño

### 4.1 `internal/config/types.go` — Tipos del spec

```go
// DevContainerConfig es la unión de las 3 variantes.
// Discriminante: IsDockerfileConfig(), IsComposeConfig(), IsImageConfig()
type DevContainerConfig struct {
    ConfigFilePath string `json:"-"` // runtime, no en JSON

    // Comunes a las 3 variantes
    Name                      *string                        `json:"name,omitempty"`
    Image                     *string                        `json:"image,omitempty"`
    ForwardPorts              []interface{}                   `json:"forwardPorts,omitempty"` // int | string
    RunArgs                   []string                       `json:"runArgs,omitempty"`
    OverrideCommand           *bool                          `json:"overrideCommand,omitempty"`
    InitializeCommand         CommandValue                   `json:"initializeCommand,omitempty"`
    OnCreateCommand           CommandValue                   `json:"onCreateCommand,omitempty"`
    UpdateContentCommand      CommandValue                   `json:"updateContentCommand,omitempty"`
    PostCreateCommand         CommandValue                   `json:"postCreateCommand,omitempty"`
    PostStartCommand          CommandValue                   `json:"postStartCommand,omitempty"`
    PostAttachCommand         CommandValue                   `json:"postAttachCommand,omitempty"`
    WaitFor                   *string                        `json:"waitFor,omitempty"`
    WorkspaceFolder           *string                        `json:"workspaceFolder,omitempty"`
    WorkspaceMount            *string                        `json:"workspaceMount,omitempty"`
    Mounts                    []MountOrString                `json:"mounts,omitempty"`
    ContainerEnv              map[string]string               `json:"containerEnv,omitempty"`
    ContainerUser             *string                        `json:"containerUser,omitempty"`
    RemoteEnv                 map[string]*string              `json:"remoteEnv,omitempty"`
    RemoteUser                *string                        `json:"remoteUser,omitempty"`
    UpdateRemoteUserUID       *bool                          `json:"updateRemoteUserUID,omitempty"`
    UserEnvProbe              *string                        `json:"userEnvProbe,omitempty"`
    Features                  map[string]interface{}          `json:"features,omitempty"`
    OverrideFeatureInstallOrder []string                     `json:"overrideFeatureInstallOrder,omitempty"`
    HostRequirements          *HostRequirements               `json:"hostRequirements,omitempty"`
    Customizations            map[string]interface{}          `json:"customizations,omitempty"`
    PortsAttributes           map[string]PortAttributes       `json:"portsAttributes,omitempty"`
    Init                      *bool                          `json:"init,omitempty"`
    Privileged                *bool                          `json:"privileged,omitempty"`
    CapAdd                    []string                       `json:"capAdd,omitempty"`
    SecurityOpt               []string                       `json:"securityOpt,omitempty"`
    AppPort                   interface{}                    `json:"appPort,omitempty"`

    // Dockerfile variant
    DockerFile *string       `json:"dockerFile,omitempty"` // legacy
    Build      *BuildConfig  `json:"build,omitempty"`
    Context    *string       `json:"context,omitempty"`

    // Compose variant
    DockerComposeFile interface{} `json:"dockerComposeFile,omitempty"` // string | []string
    Service           *string    `json:"service,omitempty"`
    RunServices       []string   `json:"runServices,omitempty"`
    ShutdownAction    *string    `json:"shutdownAction,omitempty"`
}

// CommandValue puede ser string, []string, o map[string](string|[]string)
type CommandValue = interface{}
```

**Decisión clave**: usar una struct plana con campos opcionales (punteros) en lugar de 3 structs con interfaz. Razón: Go no tiene union types; la struct plana con type guards es más ergonómica para JSON unmarshal.

### 4.2 `internal/config/loader.go` — Carga de config

```go
type LoadResult struct {
    Config          *DevContainerConfig
    Raw             map[string]interface{} // Pre-substitution
    WorkspaceConfig WorkspaceConfig
    ConfigFilePath  string
}

func LoadDevContainerConfig(host clihost.CLIHost, workspaceFolder string, configPath string, overrideConfigPath string, mountWorkspaceGitRoot bool) (*LoadResult, error)
```

**Flujo**:
1. Descubrir config: `.devcontainer/devcontainer.json` → `.devcontainer.json` (fallback).
2. Leer con `jsonc.Unmarshal`.
3. Migrar propiedades legacy (`extensions` → `customizations.vscode.extensions`).
4. Resolver workspace config (mount point, git root).
5. Aplicar variable substitution fase 1 (host-side).
6. Establecer `ConfigFilePath`.

### 4.3 `internal/config/varsub.go` — Variable substitution

**Paridad con:** `src/spec-common/variableSubstitution.ts`

**Variables soportadas:**
- `${localEnv:NAME}` / `${env:NAME}` — env del host, con default opcional: `${localEnv:NAME:default}`
- `${localWorkspaceFolder}` — path del workspace en el host
- `${localWorkspaceFolderBasename}` — basename
- `${containerWorkspaceFolder}` — path en el container
- `${containerWorkspaceFolderBasename}` — basename
- `${containerEnv:NAME}` — env del container (fase 2)
- `${devcontainerId}` — SHA256 hash de id-labels

**Fases:**
```go
// Fase 1: antes del container
func SubstituteHost(ctx HostSubContext, value interface{}) interface{}

// Fase 1b: devcontainerId
func SubstituteDevContainerID(idLabels map[string]string, value interface{}) interface{}

// Fase 2: después del container
func SubstituteContainer(platform string, containerEnv map[string]string, value interface{}) interface{}
```

**Lógica de devcontainerId:**
```go
func ComputeDevContainerID(idLabels map[string]string) string {
    // JSON.stringify con keys sorted → SHA256 → BigInt base32 padded to 52
}
```

**Regex:** `\$\{(.*?)\}` — split en `:` para separar variable de argumentos.

### 4.4 `internal/config/paths.go` — Resolución de paths

```go
func GetConfigFilePath(platform, configDir, relativePath string) string
func GetDockerfilePath(platform string, config *DevContainerConfig) string
func GetDockerComposeFilePaths(host clihost.CLIHost, config *DevContainerConfig, env map[string]string, cwd string) ([]string, error)
func IsDockerfileConfig(config *DevContainerConfig) bool
func IsComposeConfig(config *DevContainerConfig) bool
```

**Docker Compose file discovery** (cadena de fallback):
1. Propiedad `dockerComposeFile` en config (string o array).
2. `COMPOSE_FILE` env var.
3. `.env` file con `COMPOSE_FILE=...`.
4. `docker-compose.yml` + `docker-compose.override.yml` (si existe).

### 4.5 `internal/config/workspace.go` — Workspace

```go
type Workspace struct {
    IsWorkspaceFile     bool
    WorkspaceOrFolderPath string
    RootFolderPath      string
    ConfigFolderPath    string
}

type WorkspaceConfig struct {
    WorkspaceFolder string // path dentro del container
    WorkspaceMount  string // mount string
}

func WorkspaceFromPath(folderPath string) Workspace
```

## 5. Spec compliance vs. implementación

**Debe ser idéntico al spec:**
- Nombres de propiedades en devcontainer.json.
- Variables de substitution y su sintaxis.
- Orden de resolución de config path (`.devcontainer/devcontainer.json` primero).
- Docker Compose file discovery chain.
- Discriminación de variante (image vs dockerfile vs compose).

**Puede variar:**
- Estructura interna de los tipos Go.
- Cómo se almacena el raw vs substituted config.
- URI abstraction (simplificar vs vscode-uri).

## 6. Criterios de aceptación

- [ ] Todos los `devcontainer.json` en `src/test/configs/` se parsean sin error.
- [ ] `IsDockerfileConfig`/`IsComposeConfig` clasifican correctamente cada fixture.
- [ ] Variable substitution produce valores idénticos al CLI TS para los tests de `variableSubstitution.test.ts`.
- [ ] `ComputeDevContainerID` produce el mismo hash que el CLI TS para las mismas labels.
- [ ] Docker Compose file discovery sigue la cadena de fallback completa.
- [ ] Legacy property migration (`extensions` → `customizations`) funciona.
- [ ] Coverage >= 80%.

## 7. Historias de usuario

### US-E2.1: Cargar devcontainer.json con JSONC
**Como** el motor de provisioning,
**quiero** cargar un devcontainer.json que puede tener comments y trailing commas,
**para** soportar todos los formatos de config que los usuarios usan.

### US-E2.2: Discriminar variante de config
**Como** el router de provisioning,
**quiero** determinar si un config es image-based, Dockerfile-based o Compose-based,
**para** enviar al handler correcto.

### US-E2.3: Variable substitution host-side
**Como** el loader de config,
**quiero** resolver `${localEnv:HOME}` y `${localWorkspaceFolder}` antes de lanzar un container,
**para** que los paths y env vars se expandan correctamente.

### US-E2.4: Variable substitution container-side
**Como** el handler de run-user-commands,
**quiero** resolver `${containerEnv:PATH}` después de que el container esté corriendo,
**para** que los lifecycle hooks tengan acceso al env del container.

### US-E2.5: devcontainerId computation
**Como** el sistema de labels del container,
**quiero** calcular un hash determinista de los id-labels,
**para** que `${devcontainerId}` sea estable entre ejecuciones.

### US-E2.6: Docker Compose file discovery
**Como** el motor de Compose,
**quiero** descubrir los archivos docker-compose.yml siguiendo la cadena de fallback completa,
**para** soportar COMPOSE_FILE env var, .env files y defaults.

## 8. Tests a portar

- `src/test/variableSubstitution.test.ts` (170 LoC) — directamente portable.
- Config loading de cada fixture en `src/test/configs/` — golden test.
- `src/test/dockerComposeUtils.test.ts` (99 LoC) — compose file discovery.

## 9. Riesgos

| Riesgo | Mitigación |
|---|---|
| Go JSON unmarshal no soporta union types (string\|[]string para commands) | Custom UnmarshalJSON para CommandValue |
| Lógica de devcontainerId usa BigInt base32 — Go no tiene BigInt nativo | `math/big` stdlib lo cubre |
| Windows path handling en URI conversion | Tests específicos para win32 paths |
