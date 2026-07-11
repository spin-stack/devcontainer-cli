# ADR RW-003 — `devcontainer exec`: contrato de ejecución y señales

- **Estado:** Aceptada
- **Fecha:** 2026-07-11
- **Ítems relacionados:** RW-003 (contrato exec/PTY), RW-010 (Windows — cerrado por alcance)
- **Alcance del proyecto:** Linux-only, Docker-only (ver `EXECUTION-PLAN.md` §"Alcance soportado")

## Contexto

`devcontainer exec` lanza un comando dentro del contenedor de desarrollo en ejecución.
El oráculo TS (`reference/`, v0.88.0) usa `node-pty` cuando está disponible para dar
sesión interactiva con PTY; cuando `node-pty` no está presente (o el stdio no es un TTY),
cae a heredar el stdio del proceso hijo `docker exec`.

La implementación Go original arrastraba un camino de PTY autogestionado
(`internal/lifecycle/pty.go` con `github.com/creack/pty`, más `terminal*.go` para el
manejo de modo raw / ioctls de termios). Ese código tenía **cero callers en producción**:
el comando real (`internal/cli/exec.go`) nunca lo invocaba, sino que construye
`docker exec -it` (o `-i` cuando el stdio no es TTY) y hereda `os.Stdin/Stdout/Stderr`.

## Decisión

1. **exec = `docker exec -it` con stdio heredado; sin PTY autogestionado.**
   El proceso `docker` hijo hereda el terminal del CLI cuando éste corre bajo un TTY, lo
   que le da al comando remoto una PTY real provista por el propio `docker` — sin que el
   CLI tenga que gestionar `creack/pty`, modo raw ni `SIGWINCH`. Esto es
   **byte-equivalente** al fallback de TS sin `node-pty`, y por lo tanto conforme a
   paridad por definición.

2. **Borrar el código PTY muerto.** Se eliminan:
   - `internal/lifecycle/pty.go` (y `pty_test.go`)
   - `internal/lifecycle/terminal.go`, `terminal_linux.go`, `terminal_bsd.go`

   Y se retira `github.com/creack/pty` del bloque `require` directo de `go.mod`
   (queda sólo como dependencia transitiva de la infra de tests: `testcontainers-go` →
   `moby/term`). Esta ADR justifica el borrado para que nadie reintroduzca un PTY propio:
   la herencia de `docker exec -it` ya cubre el caso interactivo.

3. **Contrato de código de salida `128+N`, incluido el borde de señal al host.**
   El código de salida se propaga vía `*exec.ExitError`:
   - Salida normal → se propaga `ExitCode()` tal cual.
   - **Borde nuevo:** cuando el proceso `docker` *host* (el hijo del CLI) es terminado por
     una señal (p. ej. el terminal entrega SIGINT/SIGTERM), `os/exec` reporta
     `ExitCode() == -1`, que sin corregir se manifiesta como 255 y **diverge** del TS
     (que reporta `128+señal`, p. ej. 143 para SIGTERM). En Unix recuperamos la señal de
     `ProcessState.Sys().(syscall.WaitStatus)` y reconstruimos `128+N`. Ver
     `execExitCode()` en `internal/cli/exec.go` y su test hermético (sin Docker) en
     `internal/cli/exec_test.go`.

4. **`--log-format json` fuerza `-i` (nunca `-t`): divergencia intencional — no tocar.**
   En modo JSON el stdin viene canalizado (así invoca exec la extensión de VS Code);
   forzar `-t` haría fallar a docker con "cannot attach stdin to a TTY-enabled container".
   El stdout/stderr del comando se emiten como eventos `raw` en el stream de logs, dejando
   el stdout limpio para el consumidor JSON. Se conserva tal cual.

5. **`--terminal-columns` / `--terminal-rows` en modo interactivo** siguen al terminal
   real heredado (limitación aceptada): con stdio heredado el tamaño lo gobierna la PTY
   del terminal, no estos flags. Se mantienen sólo para paridad de superficie con TS.

## Consecuencias por plataforma (RW-010)

- **Linux:** soportado y validado. `WaitStatus.Signal()` disponible → contrato `128+N`
  completo.
- **macOS/Windows:** **no soportados** (alcance Linux-only). No se agrega código ni CI de
  Windows; no hay ConPTY. La lógica `platform="win32"` que exista se conserva únicamente
  por paridad con el oráculo TS, sin implicar soporte. Esto **cierra RW-010** como
  "no soportado — sólo documentación".

## Alternativas descartadas

- **Branch B — PTY autogestionado en Go** (`creack/pty` + termios raw + reenvío de
  `SIGWINCH`): más código y superficie de mantenimiento para replicar exactamente lo que
  `docker exec -it` ya hace vía herencia. Sin beneficio de paridad. Descartada.
