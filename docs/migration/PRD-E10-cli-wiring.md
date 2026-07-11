# PRD-E10: CLI Wiring — Cobra commands + golden tests

**Épica:** E10 — CLI Wiring
**Depende de:** Todas las anteriores (E1-E9)
**Desbloquea:** Release del CLI Go
**LoC TS equivalente:** ~1.431 (devContainersSpecCLI.ts) + handlers
**Prioridad:** P3

---

## 1. Contexto

Esta es la épica de integración final. Conecta todos los paquetes internos a los comandos CLI usando Cobra. Debe producir paridad exacta de flags, validaciones, JSON output envelopes y exit codes con el CLI TS.

**Fuente de verdad:** `docs/migration/cli-flags-inventory.yaml`

**Archivo TS principal:** `src/spec-node/devContainersSpecCLI.ts` (1.431 LoC) — todo el wiring de yargs.

## 2. Objetivo

1. **`cmd/devcontainer/main.go`** — entry point.
2. **`internal/cli/`** — un archivo por comando/subcomando con flags Cobra.
3. **100% de golden tests pasando** contra snapshots del CLI TS.
4. **Build pipeline** — Makefile/Goreleaser para cross-compilation.
5. **CI integration** — GitHub Actions workflow.

## 3. Diseño

### 3.1 Command tree (Cobra)

```
rootCmd (devcontainer)
├── upCmd
├── setUpCmd
├── buildCmd
├── runUserCommandsCmd
├── readConfigurationCmd
├── outdatedCmd
├── upgradeCmd
├── execCmd
├── featuresCmd
│   ├── featuresTestCmd
│   ├── featuresPackageCmd
│   ├── featuresPublishCmd
│   ├── featuresInfoCmd
│   ├── featuresResolveDepsCmd
│   └── featuresGenerateDocsCmd
└── templatesCmd
    ├── templatesApplyCmd
    ├── templatesPublishCmd
    ├── templatesMetadataCmd
    └── templatesGenerateDocsCmd
```

### 3.2 Flag registration pattern

Cada comando sigue este patrón:

```go
func newUpCmd() *cobra.Command {
    var opts upOptions
    cmd := &cobra.Command{
        Use:   "up",
        Short: "Create and run dev container",
        RunE:  func(cmd *cobra.Command, args []string) error {
            return runUp(cmd.Context(), &opts)
        },
    }
    // Flags exactos del CLI TS (ver cli-flags-inventory.yaml)
    cmd.Flags().StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
    cmd.Flags().StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
    cmd.Flags().StringVar(&opts.workspaceMountConsistency, "workspace-mount-consistency", "cached", "Workspace mount consistency.")
    // ... todos los flags
    return cmd
}
```

### 3.3 JSON output envelope

```go
type SuccessResult struct {
    Outcome              string      `json:"outcome"`
    ContainerID          string      `json:"containerId,omitempty"`
    RemoteUser           string      `json:"remoteUser,omitempty"`
    RemoteWorkspaceFolder string    `json:"remoteWorkspaceFolder,omitempty"`
    ComposeProjectName   *string     `json:"composeProjectName,omitempty"`
    Configuration        interface{} `json:"configuration,omitempty"`
    MergedConfiguration  interface{} `json:"mergedConfiguration,omitempty"`
    ImageName            interface{} `json:"imageName,omitempty"`
}

func writeResult(result interface{}) {
    data, _ := json.Marshal(result)
    fmt.Fprintln(os.Stdout, string(data))
}
```

**Stdout protocol:** Una línea JSON + `\n` en stdout. Logs siempre a stderr.

### 3.4 Validaciones custom

Portar las `.check()` de yargs:
```go
func validateIDLabels(labels []string) error {
    for _, label := range labels {
        if !regexp.MustCompile(`.+=.+`).MatchString(label) {
            return fmt.Errorf("id-label must match <name>=<value>")
        }
    }
    return nil
}
```

### 3.5 Exec command special handling

- `halt-at-non-option` para capturar args después de `exec`.
- TTY detection: `term.IsTerminal(int(os.Stdin.Fd()))`.
- SIGWINCH listener para resize.
- Exit code: `128 + signal` convention.

### 3.6 Yargs behavioral parity

Cobra difiere de yargs en algunos defaults:
- **boolean-negation:** Cobra no genera `--no-*` flags por defecto (matches).
- **strict mode:** Cobra por defecto rechaza flags desconocidos (matches).
- **array flags:** Cobra usa `StringArrayVar` para flags repetibles.
- **implies:** No nativo en Cobra; implementar en `PreRunE`.

### 3.7 Build & distribution

**Goreleaser config** para:
- `linux/amd64`, `linux/arm64`
- `darwin/amd64`, `darwin/arm64`
- `windows/amd64`

**Version injection:**
```bash
go build -ldflags "-X github.com/devcontainers/cli/internal/core/product.version=${VERSION}"
```

**Docker image:**
```dockerfile
FROM scratch
COPY devcontainer /usr/local/bin/devcontainer
ENTRYPOINT ["devcontainer"]
```

### 3.8 Golden test CI

```yaml
# .github/workflows/golden-tests.yml
jobs:
  golden:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
      - uses: actions/setup-go@v5
      - run: yarn && yarn compile
      - run: go build -o devcontainer ./cmd/devcontainer
      - run: ./docs/migration/golden-test-harness.sh capture
      - run: CLI_GO=./devcontainer ./docs/migration/golden-test-harness.sh compare
```

## 4. Criterios de aceptación

- [ ] Todos los comandos del `cli-flags-inventory.yaml` están implementados.
- [ ] Todos los flags con sus tipos, defaults, aliases y validaciones.
- [ ] JSON output envelopes idénticos al CLI TS.
- [ ] Exit codes: 0 (success), 1 (error), 128+N (signal).
- [ ] `--help` output cubre todos los flags (hidden flags excluidos del help).
- [ ] `--version` muestra versión inyectada.
- [ ] **100% de golden tests pasando**.
- [ ] Binarios cross-compiled para 5 targets.
- [ ] CI workflow corriendo en cada PR.

## 5. Historias de usuario

### US-E10.1: devcontainer up — full provisioning flow
### US-E10.2: devcontainer build — image building
### US-E10.3: devcontainer exec — interactive and piped
### US-E10.4: devcontainer read-configuration — config output
### US-E10.5: devcontainer set-up — existing container setup
### US-E10.6: devcontainer run-user-commands — lifecycle hooks
### US-E10.7: devcontainer outdated — version checking
### US-E10.8: devcontainer upgrade — lockfile update
### US-E10.9: devcontainer features test/package/publish/info/resolve-deps/generate-docs
### US-E10.10: devcontainer templates apply/publish/metadata/generate-docs
### US-E10.11: Cross-compilation and release pipeline
### US-E10.12: Golden test CI workflow

## 6. Tests

- **Golden tests**: la suite completa de `golden-test-harness.sh`.
- **Flag validation tests**: unit tests para cada `.check()` portada.
- **Integration tests**: E2E con Docker para `up`, `build`, `exec`.
- **No-Docker tests**: `read-configuration` con todos los fixtures.

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| Cobra/yargs behavioral differences en edge cases | Tests específicos para cada diferencia conocida |
| `exec` halt-at-non-option handling | Test con args que parecen flags (e.g., `exec ls --color`) |
| Hidden flags no deben aparecer en help | Cobra `cmd.Flags().MarkHidden()` |
| JSON output field ordering puede diferir | `json.Marshal` en Go usa field order de struct (ok) |
