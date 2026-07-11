# devcontainer-cli (Go)

A Go implementation of the [Dev Container CLI](https://containers.dev) — a drop-in
replacement for the reference TypeScript CLI, built to be distributed as a single
**static binary** and to be the primary codebase we evolve and streamline from here.

---

## Why

The reference [`devcontainers/cli`](https://github.com/devcontainers/cli) is a
Node.js/TypeScript application. Re-implementing it in Go buys us:

- **A single static binary** — no Node.js runtime, no `node_modules`, no install
  step. `CGO_ENABLED=0` gives a fully static executable that runs anywhere
  (containers `FROM scratch`, minimal CI images, air-gapped hosts).
- **Fast startup** and low overhead (no interpreter warm-up).
- **Simple distribution** — cross-compiled binaries per OS/arch, or `go install`.
- **A codebase we can evolve** — streamline the internals, add features, and change
  behavior deliberately, instead of tracking an upstream we don't control.

## What it is for

The goal is behavioral compatibility with the TS CLI for the commands people rely on:
`up`, `build`, `exec`, `read-configuration`, `set-up`, `run-user-commands`,
`features` (info/package/publish/test/…), `templates` (apply/publish/…), `outdated`,
`upgrade`. If you point tooling (including the VS Code Dev Containers extension) at
this binary instead of `node devcontainer.js`, it should behave the same.

## How parity is maintained

Parity is **not** assumed — it is validated against the real TypeScript CLI, kept in
this repo as a git submodule.

- **`reference/`** is a submodule pinned to a specific upstream release (currently
  **v0.88.0**). It is the behavioral oracle: we compile it and run the exact same
  commands through both CLIs.
- **`docs/migration/parity-matrix.yaml`** is a matrix of ~200 cases (flag validation,
  `up`/`build`/`exec`/compose/features/templates/publish scenarios). Each case runs
  the **same command** against the Go binary and the TS oracle and asserts the outputs
  match (exit code, normalized stdout/stderr, and container/registry state via
  `verify_cmd`).
- **`internal/cli/parity_matrix_test.go`** (`TestParityMatrix`) drives the matrix. It
  has two lanes:
  - **contract** — hermetic (flag/output contract, no Docker), fast.
  - **runtime** — creates real containers/images (needs Docker); parallel, isolated
    per case (unique `--id-label` / `COMPOSE_PROJECT_NAME` / `BUILDX_BUILDER`).
- **`docs/migration/GO-REWRITE-STATUS.md`** is the current parity status: what is
  covered and what remains (not a changelog — that lives in `git log`).

Unit, integration and parity tests are separate workflows. `task test:unit` only
targets `cmd/` and `internal/`, so it neither discovers packages under
`reference/node_modules` nor runs the TypeScript oracle, Docker, listeners or the
network. Parity tasks compile the reference explicitly.

### Maintaining parity as we evolve

1. When streamlining/refactoring the Go code, run the parity matrix to confirm no
   *unintended* behavior change slipped in.
2. When we *intentionally* change behavior, update the matrix case (or remove it) —
   the matrix documents deliberate divergences.
3. To track a newer upstream, bump the `reference/` submodule to the new tag and
   re-run the matrix; new failures show where upstream moved (features/behavior added
   after the last validated version).

> Note: parity was originally validated end-to-end against **v0.74.0**. The reference
> is now pinned to **v0.88.0**; running the matrix against it may surface gaps
> introduced upstream between 0.74 and 0.88 — that is expected and is the signal for
> what to bring over next.

## Layout

```
cmd/, internal/      the Go CLI implementation
scripts/             runtime assets used by the CLI (updateUID.Dockerfile)
src/test/configs/    fixtures used by the parity matrix
docs/migration/      parity audit, status log, parity-matrix.yaml
reference/           git submodule → devcontainers/cli @ v0.88.0 (parity oracle)
.github/workflows/   CI: lint/test/build/cross-compile + parity lanes
```

## Build & test

```sh
task build          # statically-linked ./devcontainer (CGO_ENABLED=0)
task test:unit      # hermetic Go unit tests
task test:integration # local HTTP integration tests
task lint           # go vet
task build:cross    # static binaries for linux/darwin/windows × amd64/arm64
```

Verify the binary is static:

```sh
file ./devcontainer        # → "statically linked"
ldd  ./devcontainer        # → "not a dynamic executable"
```

## Running the parity matrix (optional, needs the submodule + Node)

```sh
git submodule update --init reference
task reference          # install + compile the TypeScript oracle
task parity:contract    # hermetic contract lane
task parity:semantic    # semantic cases without Docker/network
task parity:network     # cases that require external network access
task parity:runtime     # complete matrix; requires Docker, ~5–6 min
```

## History

The full commit history of the rewrite (18 blockers, majors, and the parity work)
lives in the [`aledbf/cli`](https://github.com/aledbf/cli) `go-rewrite` branch. This
repo starts from that validated state with the Go code as the primary tree.
