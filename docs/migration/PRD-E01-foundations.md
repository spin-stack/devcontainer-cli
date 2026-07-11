# PRD-E01: Fundaciones

**Épica:** E1 — Fundaciones
**Depende de:** ninguna (es la primera épica)
**Desbloquea:** E2, E3, E4, E5, E6 (todas las demás épicas)
**LoC TS equivalente:** ~800
**Prioridad:** P0

---

## 1. Contexto

Todos los subsistemas del CLI dependen de un conjunto de utilidades transversales: logging, error model, abstracción de filesystem/proceso, cliente HTTP con proxy, y parsing de JSONC. En el CLI TS estas utilidades están dispersas en:

| Archivo TS | LoC | Propósito |
|---|---|---|
| `src/spec-utils/log.ts` | 322 | Logger con niveles, formatos text/json, dimensiones de terminal |
| `src/spec-common/errors.ts` | 63 | `ContainerError` con metadata de remediación |
| `src/spec-common/cliHost.ts` | 279 | Abstracción de host (fs, exec, pty, system info) |
| `src/spec-utils/httpRequest.ts` | 161 | Cliente HTTP con proxy y redirects |
| `src/spec-utils/pfs.ts` | 50 | Utilidades de filesystem local |
| `src/spec-utils/event.ts` | 42 | Event emitter |
| `src/spec-common/async.ts` | 8 | delay() |
| `src/spec-utils/strings.ts` | 9 | String utilities |
| `src/spec-utils/product.ts` | 15 | Package config (version, name) |

**Nota**: `commonUtils.ts` (598 LoC) contiene tanto las fundaciones (exec/PTY) como lógica de provisioning. Solo la parte de exec/signal entra en E1; el resto se aborda en E6/E8.

## 2. Objetivo

Al finalizar E1, existirán los siguientes paquetes Go listos para usar por las épicas posteriores:

- `internal/core/log` — Logger con modos text y JSON
- `internal/core/errors` — `ContainerError` con metadata estructurada
- `internal/core/clihost` — Interfaz CLIHost (filesystem, exec, system info)
- `internal/core/httpx` — Cliente HTTP con proxy automático
- `internal/core/jsonc` — Wrapper de JSONC parser
- `internal/core/pfs` — Filesystem utilities
- `internal/core/product` — Version/nombre del CLI

## 3. No-objetivos

- PTY handling (va en E8)
- Shell server / command serialization (va en E8)
- Variable substitution (va en E2)
- Cualquier lógica de Docker, OCI, o features

## 4. Diseño detallado por paquete

### 4.1 `internal/core/log`

**Paridad con:** `src/spec-utils/log.ts`

#### Interfaz pública

```go
type LogLevel int

const (
    LogLevelTrace LogLevel = iota
    LogLevelDebug
    LogLevelInfo
    LogLevelWarning
    LogLevelError
    LogLevelCritical
    LogLevelOff
)

type LogDimensions struct {
    Columns int
    Rows    int
}

type LogEvent struct {
    Type      string      // "text", "raw", "start", "stop", "progress"
    Timestamp time.Time
    Level     LogLevel
    Channel   string      // optional grouping
    Text      string
    // For "progress" events:
    Name      string
    Status    string      // "running", "succeeded", "failed"
}

type Log interface {
    Write(text string, level ...LogLevel)
    // Structured events
    Start(name string)
    Stop(name string, duration time.Duration)
    Progress(name string, status string)
    // Configuration
    Dimensions() LogDimensions
}

// Factory
func NewLog(opts LogOptions) Log

type LogOptions struct {
    Level      LogLevel
    Format     string // "text" | "json"
    Writer     io.Writer
    Dimensions *LogDimensions
    // Para header de sesión
    Version    string
    StartTime  time.Time
}
```

#### Comportamiento text mode
- Escribe a `os.Stderr` por defecto.
- Prefija con `[{elapsed_ms} ms]` igual que el CLI TS.
- Usa `fatih/color` para colorización cuando el writer es TTY.

#### Comportamiento json mode
- Emite una línea JSON por evento: `{"type":"text","timestamp":"...","level":"info","text":"..."}`
- Idéntico formato al CLI TS para consumo por VS Code.

#### Tests
- Unit test: verificar que cada nivel filtra correctamente.
- Unit test: verificar formato JSON parseable.
- Fixture: comparar output de log contra snapshot del CLI TS con `--log-format json`.

---

### 4.2 `internal/core/errors`

**Paridad con:** `src/spec-common/errors.ts`

#### Interfaz pública

```go
type ContainerError struct {
    Description        string
    OriginalError      error
    ContainerID        string
    DisallowedFeatureID string
    DidStopContainer   bool
    LearnMoreURL       string
}

func (e *ContainerError) Error() string
func (e *ContainerError) Unwrap() error

// Serialización JSON para output envelope
type ErrorOutput struct {
    Outcome            string `json:"outcome"`            // siempre "error"
    Message            string `json:"message"`
    Description        string `json:"description"`
    ContainerID        string `json:"containerId,omitempty"`
    DisallowedFeatureID string `json:"disallowedFeatureId,omitempty"`
    DidStopContainer   *bool  `json:"didStopContainer,omitempty"`
    LearnMoreURL       string `json:"learnMoreUrl,omitempty"`
}

func ToErrorOutput(err error) ErrorOutput
```

#### Tests
- Unit test: verificar que `ToErrorOutput` produce JSON idéntico al CLI TS.
- Unit test: verificar que `Unwrap` preserva la cadena de errores.

---

### 4.3 `internal/core/clihost`

**Paridad con:** `src/spec-common/cliHost.ts`

#### Interfaz pública

```go
type CLIHost interface {
    // Filesystem
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte) error
    MkdirAll(path string) error
    ReadDir(path string) ([]os.DirEntry, error)
    Stat(path string) (os.FileInfo, error)
    IsFile(path string) (bool, error)
    Rename(old, new string) error
    Remove(path string) error

    // System info
    Platform() string       // "linux", "darwin", "win32" (compat con Node)
    Arch() string           // "x64", "arm64" (compat con Node)
    HomeDir() string
    TmpDir() string
    Cwd() string
    Username() string
    UID() int
    GID() int
    Env() map[string]string

    // Process execution
    Exec(params ExecParams) (*ExecResult, error)

    // Path utilities
    Path() PathUtils
}

type ExecParams struct {
    Cmd  string
    Args []string
    Cwd  string
    Env  map[string]string
}

type ExecResult struct {
    Stdout   []byte
    Stderr   []byte
    ExitCode int
}

type PathUtils interface {
    Join(parts ...string) string
    Resolve(parts ...string) string
    Dirname(p string) string
    Basename(p string) string
}

// Factory
func NewLocalCLIHost(cwd string) (CLIHost, error)
```

#### Decisiones de diseño
- `Platform()` y `Arch()` devuelven nombres compatibles con Node.js (`win32` no `windows`, `x64` no `amd64`) para mantener compatibilidad con variable substitution y metadata labels.
- Mapping interno: `runtime.GOOS` → Node names, `runtime.GOARCH` → Node names.
- `Exec` usa `os/exec` de stdlib. No PTY (eso va en E8).
- No implementamos hosts remotos (SSH, WSL) — solo local. La interfaz permite extensión futura.

#### Tests
- Unit test: filesystem operations en temp dir.
- Unit test: Platform/Arch mapping correcto.
- Unit test: Exec captura stdout/stderr/exitcode.

---

### 4.4 `internal/core/httpx`

**Paridad con:** `src/spec-utils/httpRequest.ts` + `proxy-agent`

#### Interfaz pública

```go
type HTTPClient struct {
    // Configured with proxy and CA certs
}

type RequestOptions struct {
    Method  string // GET, HEAD, POST, PUT, PATCH, DELETE
    URL     string
    Headers map[string]string
    Body    []byte
}

type Response struct {
    StatusCode int
    Headers    http.Header
    Body       []byte
}

func NewHTTPClient() *HTTPClient
func (c *HTTPClient) Do(opts RequestOptions) (*Response, error)
```

#### Comportamiento
- Respeta `http_proxy`, `https_proxy`, `no_proxy` via `httpproxy.Config` de stdlib.
- Carga `NODE_EXTRA_CA_CERTS` si existe (para compat — renombrar a `SSL_CERT_FILE` también).
- Sigue redirects automáticamente (stdlib `http.Client` ya lo hace).
- User-Agent: `devcontainer-cli/{version}`.
- Timeout configurable (default 30s).

#### Tests
- Unit test con `httptest.NewServer`: GET, POST, redirect, proxy.
- Integration test: fetch un manifiesto real de ghcr.io (skip en CI sin red).

---

### 4.5 `internal/core/jsonc`

**Paridad con:** dependencia `jsonc-parser`

#### Interfaz pública

```go
// Parse JSON with comments and trailing commas into raw bytes
// that are valid JSON (comments stripped, trailing commas removed).
func StripComments(input []byte) ([]byte, error)

// Parse directly into a target struct
func Unmarshal(input []byte, v interface{}) error

// Parse into generic map (for dynamic access)
func Parse(input []byte) (map[string]interface{}, error)
```

#### Dependencia
- `github.com/tailscale/hujson` — Maneja Human JSON (comments + trailing commas). Bien mantenido.

#### Spike requerido
Antes de elegir, validar contra TODOS los `devcontainer.json` en `src/test/configs/`:
```bash
find src/test/configs -name "devcontainer.json" -exec echo {} \;
```
Cada fixture debe parsearse correctamente con la librería elegida.

#### Tests
- Unit test: JSON con `//` comments, `/* */` comments, trailing commas.
- Unit test: JSON inválido produce error.
- Unit test: Parse de cada fixture de `src/test/configs/`.

---

### 4.6 `internal/core/pfs`

**Paridad con:** `src/spec-utils/pfs.ts`

```go
func ReadLocalFile(path string) ([]byte, error)
func WriteLocalFile(path string, data string) error
func MkdirpLocal(path string) error
func RmLocal(path string, opts RmOptions) error
func IsLocalFile(path string) (bool, error)

type RmOptions struct {
    Recursive bool
    Force     bool
}
```

Wrapper thin sobre `os` stdlib. Existe para mantener names consistentes con el CLI TS y facilitar testing con interfaces.

---

### 4.7 `internal/core/product`

**Paridad con:** `src/spec-utils/product.ts`

```go
type PackageConfig struct {
    Name    string
    Version string
}

func GetPackageConfig() PackageConfig
```

Version se inyecta en build time via `-ldflags`:
```bash
go build -ldflags "-X internal/core/product.version=0.74.0"
```

---

## 5. Criterios de aceptación

- [ ] `internal/core/log`: Logger text y JSON producen output parseado correctamente. Niveles filtran. Dimensiones se propagan.
- [ ] `internal/core/errors`: `ContainerError` serializa a JSON idéntico al CLI TS (verificar con golden test fixture).
- [ ] `internal/core/clihost`: `NewLocalCLIHost` funciona en linux, darwin, windows. `Exec` captura exit codes no-cero.
- [ ] `internal/core/httpx`: Requests con proxy funcionan. Redirects se siguen. CA certs custom se cargan.
- [ ] `internal/core/jsonc`: Todos los `devcontainer.json` de fixtures parsean sin error.
- [ ] `internal/core/pfs`: Read/write/mkdir/rm operaciones básicas.
- [ ] `internal/core/product`: Version inyectada en build time.
- [ ] Coverage >= 80% en cada paquete.
- [ ] `go vet ./...` y `golangci-lint run` sin errores.

## 6. Historias de usuario

### US-E1.1: Logger text mode

**Como** desarrollador del CLI Go,
**quiero** un logger que escriba a stderr con formato `[{ms} ms] {message}`,
**para** que la salida sea idéntica al CLI TS en modo text.

**Criterios de aceptación:**
- Given log level = info, format = text
  When escribo "Starting container"
  Then stderr contiene `[X ms] Starting container`
- Given log level = debug
  When escribo un mensaje trace
  Then no aparece en stderr

**Referencia TS:** `src/spec-utils/log.ts:1-322`
**Tests a portar:** formato de output comparado con snapshot

### US-E1.2: Logger JSON mode

**Como** integrador del CLI (VS Code, Codespaces),
**quiero** un logger que emita eventos JSON a stderr,
**para** parsear el progreso programáticamente.

**Criterios de aceptación:**
- Given log format = json
  When escribo un mensaje info
  Then stderr contiene una línea JSON con `{"type":"text","level":"info","text":"..."}`
- Given log format = json
  When emito un evento progress
  Then stderr contiene `{"type":"progress","name":"...","status":"running"}`

**Referencia TS:** `src/spec-utils/log.ts` (LogEvent types)

### US-E1.3: ContainerError model

**Como** handler de comando CLI,
**quiero** crear errores tipados con metadata (containerId, learnMoreUrl, etc.),
**para** producir el JSON error envelope estándar del CLI.

**Criterios de aceptación:**
- Given un ContainerError con Description="An error occurred"
  When serializo a JSON
  Then el output contiene `{"outcome":"error","message":"...","description":"An error occurred"}`

**Referencia TS:** `src/spec-common/errors.ts:1-63`

### US-E1.4: CLIHost local filesystem

**Como** cualquier subsistema del CLI,
**quiero** leer/escribir archivos y ejecutar procesos via una interfaz abstracta,
**para** que el código sea testeable sin acceder al fs real.

**Criterios de aceptación:**
- Given un CLIHost local
  When llamo ReadFile("devcontainer.json")
  Then obtengo el contenido del archivo
- Given un CLIHost local
  When llamo Exec({Cmd: "echo", Args: ["hello"]})
  Then ExecResult.Stdout = "hello\n" y ExitCode = 0

**Referencia TS:** `src/spec-common/cliHost.ts:1-279`

### US-E1.5: HTTP client con proxy

**Como** el sistema OCI o feature fetch,
**quiero** hacer requests HTTP que respeten proxy del entorno,
**para** funcionar en redes corporativas.

**Criterios de aceptación:**
- Given http_proxy=http://proxy:8080
  When hago GET a https://ghcr.io/v2/
  Then el request se enruta por el proxy
- Given NODE_EXTRA_CA_CERTS=/path/to/cert.pem
  When hago HTTPS request
  Then el cert custom se usa para TLS

**Referencia TS:** `src/spec-utils/httpRequest.ts:1-161`

### US-E1.6: JSONC parser

**Como** el loader de devcontainer.json,
**quiero** parsear JSON con comments (`//`, `/* */`) y trailing commas,
**para** que todos los devcontainer.json del mundo real sean aceptados.

**Criterios de aceptación:**
- Given `{ "image": "ubuntu" /* comment */ }` (con comment)
  When parseo con JSONC
  Then obtengo `{"image":"ubuntu"}`
- Given todos los fixtures en `src/test/configs/*/devcontainer.json`
  When parseo cada uno
  Then ninguno produce error

**Referencia TS:** dependencia `jsonc-parser`

### US-E1.7: Product version

**Como** el comando `devcontainer --version`,
**quiero** que la versión se inyecte en build time,
**para** no tener que leer package.json.

**Criterios de aceptación:**
- Given binario compilado con `-ldflags "-X ...version=1.0.0"`
  When llamo GetPackageConfig()
  Then Version = "1.0.0"

**Referencia TS:** `src/spec-utils/product.ts`

## 7. Definition of Done (épica completa)

- [ ] Todos los paquetes listados existen en `internal/core/`
- [ ] Cada paquete tiene >= 80% coverage
- [ ] `go build ./...` compila sin errores en linux, darwin, windows
- [ ] `golangci-lint run` sin warnings
- [ ] Spike de JSONC validado contra todos los fixtures
- [ ] PR aprobado y mergeado

## 8. Riesgos

| Riesgo | Mitigación |
|---|---|
| `hujson` no maneja algún edge case de JSONC | Spike antes de implementar. Fallback: escribir strip propio (~200 LoC). |
| Platform/Arch mapping tiene edge cases en Windows ARM | Tabla exhaustiva con tests. Baja prioridad (Windows ARM es raro). |
| `NODE_EXTRA_CA_CERTS` no aplica directamente en Go | Mapear a `crypto/tls` custom CA pool. Go también respeta `SSL_CERT_FILE`. |

## 9. Estimación

**T-shirt size:** S (la más pequeña de todas las épicas)
**Razón:** Son wrappers thin sobre stdlib de Go. La complejidad real está en las épicas que consumen estos paquetes.

Esta épica es deliberadamente minimalista — su valor es establecer las interfaces que las demás épicas usarán. No añade features, solo infraestructura.
