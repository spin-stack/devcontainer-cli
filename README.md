# devcontainer-cli (Go)

A Go implementation of the [Dev Container CLI](https://containers.dev), built for
behavioral parity with the reference TypeScript implementation and now the primary
codebase to evolve and streamline.

## Layout

- `cmd/`, `internal/` — the Go CLI implementation.
- `scripts/` — runtime assets used by the CLI (e.g. `updateUID.Dockerfile`).
- `docs/migration/` — the parity audit, status log (`GO-REWRITE-STATUS.md`) and the
  parity matrix (`parity-matrix.yaml`) that validated behavioral parity vs the TS CLI.
- `reference/` — git submodule pinned to `devcontainers/cli` **v0.74.0**, the upstream
  TypeScript CLI kept as a behavioral reference and parity oracle.

## Build & test

```sh
task build          # build ./devcontainer
task test           # go unit tests
task lint           # go vet
```

## Parity (optional, against the reference submodule)

The parity matrix compares the Go binary against the TypeScript oracle. It requires
the reference submodule and its compiled `dist/`:

```sh
git submodule update --init reference
task ts:install     # yarn install in reference/
task ts:compile     # yarn compile in reference/ → reference/dist
task parity:matrix:contract   # hermetic contract lane
task parity:matrix:runtime    # runtime lane (requires Docker)
```

The unit test `TestParityMatrix` skips automatically when `reference/dist` is absent.
