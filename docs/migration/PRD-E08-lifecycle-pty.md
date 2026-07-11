# PRD-E08: Lifecycle hooks, exec, PTY y dotfiles

**Épica:** E8 — Lifecycle + PTY
**Depende de:** E6 (Docker engine), E9 (Image metadata — para MergedDevContainerConfig)
**Desbloquea:** E10 (CLI wiring — exec command)
**LoC TS equivalente:** ~1.400
**Prioridad:** P2

> **Nota sobre dependencia con E9:** Los lifecycle hooks operan sobre `MergedDevContainerConfig`, no sobre el config raw. El merge semántico (lifecycle hooks concatenados desde features + config, `remoteUser` resuelto, `remoteEnv` combinado, `waitFor` heredado) se define en E9. E8 **consume** esa estructura pero no la produce. Si se implementa E8 antes de E9, usar una interfaz `MergedConfig` mínima con los campos necesarios (`OnCreateCommand`, `PostCreateCommand`, `RemoteUser`, `RemoteEnv`, `WaitFor`, etc.) que E9 luego satisfaga completamente.

---

## 1. Contexto

Después de provisionar un container, el CLI ejecuta lifecycle hooks (onCreateCommand, postCreateCommand, etc.), instala dotfiles, sondea el env remoto, y permite ejecutar comandos interactivos via `devcontainer exec`. Todo esto requiere un "shell server" que serializa comandos dentro del container, y PTY handling para sesiones interactivas.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-common/injectHeadless.ts` | 963 | Orquestación de lifecycle, env probe, setupInContainer |
| `src/spec-common/shellServer.ts` | 199 | Shell server con EOT markers |
| `src/spec-common/proc.ts` | 69 | Process tree inspection via /proc |
| `src/spec-common/dotfiles.ts` | 130 | Dotfiles clone + install |
| `src/spec-common/commonUtils.ts` | ~200 (partial) | PTY exec, signal mapping |

## 2. Objetivo

Paquetes que:
1. **Shell server**: Lancen `/bin/sh` dentro del container via `docker exec` y serialicen comandos con delimitadores EOT (`\u2404`).
2. **Lifecycle hooks**: Ejecuten la secuencia completa (initializeCommand → onCreateCommand → updateContentCommand → postCreateCommand → postStartCommand → postAttachCommand) respetando `waitFor`, `skipPostCreate`, `prebuild`, etc.
3. **Remote env probe**: Sondeen user, env vars, home folder, platform del container.
4. **Dotfiles**: Clonen y ejecuten dotfiles repository.
5. **PTY exec**: Para `devcontainer exec`, manejen sesiones interactivas con PTY.

## 3. Diseño

### 3.1 `internal/lifecycle/shell.go` — Shell server

```go
const EOT = '\u2404'

type ShellServer struct {
    process *exec.Cmd
    stdin   io.Writer
    stdout  io.Reader
    stderr  io.Reader
    mu      sync.Mutex // serializa comandos
}

func LaunchShellServer(dockerClient *docker.Client, containerID string, platform string) (*ShellServer, error)
func (s *ShellServer) Exec(cmd string, opts ExecOpts) (stdout string, stderr string, err error)
func (s *ShellServer) Close() error
```

**Protocolo** (Linux):
1. Enviar: `echo -n {EOT}; ( {cmd} ); echo -n {EOT}$?{EOT}; echo -n {EOT} >&2\n`
2. Leer stdout hasta segundo EOT → captura output + exit code.
3. Leer stderr hasta EOT.

**Serialización**: los comandos se encolan; solo uno ejecuta a la vez.

### 3.2 `internal/lifecycle/hooks.go` — Lifecycle execution

```go
type LifecyclePhase string

const (
    PhaseInitialize    LifecyclePhase = "initializeCommand"
    PhaseOnCreate      LifecyclePhase = "onCreateCommand"
    PhaseUpdateContent LifecyclePhase = "updateContentCommand"
    PhasePostCreate    LifecyclePhase = "postCreateCommand"
    PhasePostStart     LifecyclePhase = "postStartCommand"
    PhasePostAttach    LifecyclePhase = "postAttachCommand"
)

func RunLifecycleHooks(params *ResolverParams, containerProps *ContainerProperties, config *MergedConfig, remoteEnv map[string]string, secrets map[string]string) (*LifecycleResult, error)
func SetupInContainer(params *CommonParams, containerProps *ContainerProperties, config *DevContainerConfig, mergedConfig *MergedConfig) (*SetupResult, error)
```

**Secuencia:**
1. `initializeCommand` — ejecutado en HOST, no en container.
2. Remaining hooks ejecutados en container via shell server.
3. `waitFor` controla cuándo el IDE puede attached (default: `updateContentCommand`).
4. `postStartCommand` y `postAttachCommand` pueden ser non-blocking.

**Command format**: cada hook puede ser `string`, `[]string` (exec form), o `map[string](string|[]string)` (parallel execution).

### 3.3 `internal/lifecycle/probe.go` — Remote env probe

```go
func ProbeRemoteEnv(params *CommonParams, containerProps *ContainerProperties, config *MergedConfig) (map[string]string, error)
```

**Estrategias** (según `userEnvProbe`):
- `none`: no probe.
- `loginInteractiveShell`: `docker exec -it ... bash -lic 'env'`.
- `interactiveShell`: `docker exec -it ... bash -ic 'env'`.
- `loginShell`: `docker exec ... bash -lc 'env'`.

Parsea el output como `KEY=VALUE` lines.

### 3.4 `internal/lifecycle/dotfiles.go` — Dotfiles installation

```go
func InstallDotfiles(params *ResolverParams, containerProps *ContainerProperties, dockerEnv map[string]string, secrets map[string]string) error
```

**Flujo:**
1. Check marker file (no reinstalar si ya existe).
2. `git clone --depth 1 {repository} {targetPath}`.
3. Si `installCommand` → ejecutar.
4. Si no → buscar en orden: `install.sh`, `install`, `bootstrap.sh`, `bootstrap`, `script/bootstrap`, `setup.sh`, `setup`, `script/setup`.
5. Si nada encontrado → symlink dotfiles (`ln -sf`).

### 3.5 `internal/docker/pty.go` — PTY para exec

```go
func ExecWithPTY(dockerClient *docker.Client, containerID string, cmd []string, env map[string]string, cwd string, dimensions *LogDimensions) (exitCode int, err error)
```

**Implementación:**
- Linux/macOS: `github.com/creack/pty` para crear un pseudo-terminal.
- Windows: `github.com/UserExistsError/conpty` (spike necesario).
- Manejo de SIGWINCH para resize.
- stdin/stdout/stderr forwarding bidireccional.

**Para `devcontainer exec`:**
- Si stdin y stdout son TTY → usar PTY.
- Si pipe → usar non-PTY docker exec con stream forwarding.

### 3.6 Signal handling

```go
var ProcessSignals = map[string]int{
    "SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, /* ... */
}
```

Exit code: `128 + signal_number` convention.

## 4. Criterios de aceptación

- [ ] Shell server ejecuta comandos y captura stdout/stderr/exit code correctamente.
- [ ] Lifecycle hooks se ejecutan en el orden correcto.
- [ ] `waitFor` detiene la secuencia en el hook configurado.
- [ ] `skipPostCreate`, `prebuild`, `skipNonBlocking` flags funcionan.
- [ ] Remote env probe captura variables correctamente con cada estrategia.
- [ ] Dotfiles se instalan y el marker file previene reinstalación.
- [ ] `exec` con PTY funciona en macOS y Linux.
- [ ] `exec` sin PTY (piped) funciona.
- [ ] Exit codes propagados correctamente (128+signal).
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E8.1: Run lifecycle hooks in correct order
### US-E8.2: Execute commands in container via shell server
### US-E8.3: Probe remote environment with configured strategy
### US-E8.4: Install dotfiles from git repository
### US-E8.5: Interactive exec with PTY
### US-E8.6: Non-interactive exec with piped I/O
### US-E8.7: Handle waitFor and blocking/non-blocking commands
### US-E8.8: Propagate signals and exit codes

## 6. Tests a portar

- `src/test/container-features/lifecycleHooks.test.ts` (461 LoC) — hook ordering.
- `src/test/dotfiles.test.ts` (120 LoC).
- `src/test/cli.exec.*.test.ts` (4 files, ~450 LoC total) — exec with/without BuildKit.
- Golden tests para lifecycle execution.

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| PTY en Windows (conpty) no es maduro en Go | Spike. Documentar limitaciones. Fallback a non-PTY. |
| Shell server EOT protocol edge cases (output contains EOT char) | El EOT char `\u2404` es raro en output normal. Monitor en tests. |
| Remote env probe timeout/hang con shells lentos | Timeout configurable (30s default). |
