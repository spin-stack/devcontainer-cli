# RW-011 — Small seams for external effects

Status: accepted · Track: T5 (keystone) · Scope: Linux-only, Docker-only

## Context

Before this change the Go CLI had exactly **one** seam over an external effect:
`lifecycle.CommandExecutor`. Everything else — stdout/stderr, process execution,
OCI registry access, and the filesystem — was a concrete dependency wired inline.

The practical cost showed up in the tests: to assert on command output they swapped
the global `os.Stdout` (`old := os.Stdout; os.Stdout = w; ...`). That is racy, it
cannot run under `t.Parallel()`, and it only captures effects that happen to go
through `os.Stdout`. The publish, template-apply, and feature-resolution paths could
not be exercised at all without a real registry or real disk.

## Decision

Introduce **four small interfaces**, each listing only the methods actually called,
and inject them per command via the existing `buildRunner`-style structs (and, for
free functions, as an explicit parameter). No behavior changes — these are seams.

### 1. `cli.Output` — stdout/stderr (`internal/cli/output.go`)

```go
type Output interface {
    Stdout() io.Writer
    Stderr() io.Writer
}
```

- Default impl `OSOutput()` returns `os.Stdout`/`os.Stderr`.
- `outputFor(cmd)` adapts a `*cobra.Command`, **reusing Cobra's
  `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`** so `cmd.SetOut`/`SetErr` (and thus a
  test buffer) flow through for free.
- The four result helpers in `build.go` (`writeSuccessJSON`, `writeErrorResult`,
  `writeErrorJSON`, `writeValidationError`) take an `Output` as their first argument
  instead of hardcoding `os.Stdout`/`os.Stderr`. Every `up`/`build`/`set-up`/
  `run-user-commands`/`read-configuration` call site threads it through.
- The ~25 direct `fmt.Println` / `Fprintln(os.Stdout, …)` sites in
  `features_info.go`, `features_resolve_deps.go`, `templates_metadata.go`,
  `templates_apply.go`, `collection_commands.go`, `features_test_runner.go`, and
  `outdated.go` now write to `out.Stdout()`.

### 2. `oci.Registry` — registry operations (`internal/oci/registry.go`)

```go
type Registry interface {
    FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error)
    FetchBlob(ref *Ref, digest string) ([]byte, error)
    GetPublishedTags(ref *Ref) ([]string, error)
    PushArtifact(ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error)
    PushCollectionMetadata(ref *Ref, collectionJSONPath string) (*PushResult, error)
}
```

- **Exactly** the five methods consumers use. `*oci.Client` already satisfies it
  (compile-time asserted). The `…Context` variants stay on `*Client` for callers
  that need cancellation; they are intentionally not in the interface.
- `templates.ApplyParams.OCIClient` and the CLI helpers that received a
  `*oci.Client` (`renderDependencyMermaid`, `resolvePublishedVersions`,
  `resolveFeatureSets`, `fetchFeatureSets`) now accept `oci.Registry`, so a fake can
  be injected. `fetchFeatureSets` takes the registry as a parameter (nil ⇒ default
  client) rather than constructing one inline.

### 3. `exec.Runner` — process execution (`internal/exec/exec.go`)

```go
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, code int, err error)
}
```

- `OSRunner` is the default, backed by `os/exec`, replicating the existing env
  handling (`append(os.Environ(), Env...)` when `Env` is non-empty).
- Contract: a command that **runs and exits non-zero** returns that exit code with
  a `nil` error; `err` is non-nil only when the process could not be started. This
  matches what `docker.Client.Run`/`ComposeClient.Run` already did.
- Wired into the two highest-value one-shot callers: `docker.Client.Run` and
  `docker.ComposeClient.Run` (both gained a `Runner` field; nil ⇒ `OSRunner`).

### 4. `pfs.FS` — filesystem (`internal/pfs/fs.go`)

```go
type FS interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte) error
    MkdirAll(path string) error
    Stat(path string) (os.FileInfo, error)
    Walk(root string, fn filepath.WalkFunc) error
    Remove(path string, recursive bool) error
}
```

- `OSFS` delegates to the existing `pfs` free functions; `DefaultFS()` returns it.
- `internal/templates/apply.go` (`FetchAndApply`, `mergeFeatures`,
  `applyOptionDefaults`) routes its file operations through `ApplyParams.FS`
  (nil ⇒ default). This makes the "workspace half-written after a WriteFile failure"
  path testable with a fake (the RW-012 follow-up).

## Rejected alternative: a monolithic `CLIHost`

A single `CLIHost` bundling stdout + process + OCI + filesystem was explicitly
rejected. It couples unrelated effects, forces every consumer and every fake to
depend on capabilities it does not use, grows without back-pressure (a "god
interface"), and obscures which effect a given call site actually performs. Four
small, single-purpose interfaces keep each fake trivial, keep the dependency edges
honest, and let a consumer depend on just the effect it uses.

Guiding rule for all four: **list only methods actually called** — no speculative
surface. This lands the churn once.

## Deliberately left un-seamed

- **Shell server / interactive `docker exec`** (`lifecycle/shell.go`,
  `initialize.go`, PTY path): these are long-lived, streaming, stdin-attached
  processes, not the one-shot capture that `exec.Runner.Run` models. Forcing them
  through `Runner` would distort the interface. They keep their direct `exec.Command`
  usage; `lifecycle.CommandExecutor` remains their seam.
- **`ComposeClient` version detection** (`NewComposeClient`): a construction-time
  `docker compose version` probe using `.Output()`. Low value, runs once; left on
  `os/exec`.
- **`root.go` top-level error print** (`Fprintln(os.Stderr, err)`): the outermost
  fallback when a command returns an error, with no command-scoped `Output` in a
  meaningful sense. Left as-is.
- **oras transport injection**: out of scope here; tracked by RW-014.

## Consequences

- Tests capture output via `cmd.SetOut(&buf)` / a fake `Output` instead of swapping
  `os.Stdout` (the read-configuration test was converted).
- Partial-publish, template-apply-failure, and exec paths are now unit-testable with
  fakes — the foundation RW-012 and RW-014 build on.
- Hermetic tests added: `TestPublishCollectionPartialFailure` (fake `oci.Registry`
  driving the partial-publish error path with captured stdout) and
  `TestRunUsesRunnerSeam` (fake `exec.Runner` injected into `docker.Client`).
