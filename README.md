# devcontainer (Go)

**A single static binary that runs your [dev containers](https://containers.dev) —
a drop-in replacement for the official `@devcontainers/cli` on **Linux + Docker +
Docker Compose v2**, with none of the Node.js baggage and a few superpowers the
original doesn't have.**

Point your tooling at this `devcontainer` binary instead of `node devcontainer.js`
and, within its validated scope — the seven command families in the parity matrix
(`up`, `build`, `exec`, `read-configuration`, `run-user-commands`, `features`,
`templates`) on Linux/Docker/Compose v2 — it behaves the same, except it starts
faster, ships as one file, and fixes the papercuts you hit every day. The deliberate
differences (Podman and macOS/Windows are out of scope; distinct `--override-config`
merge and terminal logging; no legacy-Feature fallback) are recorded in
[Divergences](docs/DIVERGENCES.md).

---

## Why switch?

If you use the official CLI, you already know the friction:

| Daily pain with `@devcontainers/cli` | With this binary |
| --- | --- |
| `npm i -g @devcontainers/cli` pulls a Node runtime + hundreds of MB of `node_modules`, and drifts out of sync across machines/CI | **One static binary.** `CGO_ENABLED=0`, no runtime, no `node_modules`. Drop it in `FROM scratch`, a distroless CI image, or an air-gapped host. |
| Node interpreter warm-up on every invocation | **Fast cold start**, low overhead. |
| Pulling a **private base image** or `--push`/`--cache-to` to a private registry means running `docker login` by hand — sometimes in several places | **Credential bridge:** the CLI's own auth chain (`DEVCONTAINERS_OCI_AUTH`, cred helpers, `GITHUB_TOKEN`) is handed to `docker build` automatically. |
| A missing buildx, no cache-export, or a wrong Compose version surfaces as a cryptic error **deep into a build** | **`devcontainer check`** preflights your host and tells you exactly what's wrong (and `setup` fixes what it safely can). |
| Reusing a prebuilt image means re-implementing config hashing and clone-and-mutate hacks in your orchestrator | **`--cache-key`** emits a deterministic content hash; **`up --cache-image`** boots from a prebuilt image and skips feature install. |
| A TLS-intercepting proxy breaks image pulls because the CA isn't honored everywhere | Proxy + `NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE` honored on **every** request path. |

Same behavior where it counts, better ergonomics where it hurts.

## Install

Build it yourself — it's one command, and the result is a single self-contained file:

```sh
git clone https://github.com/spin-stack/devcontainer-cli
cd devcontainer-cli
task build            # or: go build -o devcontainer ./cmd/devcontainer
sudo mv devcontainer /usr/local/bin/
```

Tagged releases publish prebuilt **static** binaries (Linux amd64/arm64) and a
container image to the
[Releases page](https://github.com/spin-stack/devcontainer-cli/releases) — grab those
instead once a version is cut.

Confirm it's a single self-contained file:

```sh
file ./devcontainer   # → "statically linked"
ldd  ./devcontainer   # → "not a dynamic executable"
```

> **Scope: Linux + Docker.** Supported and validated on **Linux** (amd64, arm64) with
> **Docker** and Compose **v2**. Windows, macOS and Podman are not targets.

## Hello, dev container (60 seconds)

Create a project with a minimal dev container config:

```sh
mkdir hello-devcontainer && cd hello-devcontainer
mkdir -p .devcontainer
cat > .devcontainer/devcontainer.json <<'JSON'
{
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",
  "features": {
    "ghcr.io/devcontainers/features/node:1": {}
  },
  "postCreateCommand": "node --version"
}
JSON
```

Bring it up, then run a command inside it:

```sh
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . bash -lc 'node --version && whoami'
```

That's it — a real container, your feature installed, your workspace bind-mounted.
Any automation that shells out to the `devcontainer` CLI (CI pipelines, prebuild
services, scripts) can call this binary instead of `node devcontainer.js`.

## Superpowers (Go-only)

These commands and flags don't exist in the upstream CLI. Full reference:
**[docs/go-only-features.md](docs/go-only-features.md)**.

```sh
# Preflight the host — daemon, buildx, cache export, Compose v2, disk — before you build
devcontainer check
devcontainer setup            # apply the safe fixes (e.g. a cache-capable buildx builder)

# Deterministic cache key for prebuild reuse (hash of config + Dockerfile + features + proxy)
devcontainer read-configuration --workspace-folder . --cache-key

# Boot from a prebuilt image, skipping build + feature install
devcontainer up --workspace-folder . --cache-image ghcr.io/acme/app-devcontainer:sha-abc123

# Pass BuildKit build secrets (RUN --mount=type=secret,id=…) without baking them into a layer
devcontainer build --workspace-folder . --secrets-file secrets.json

# Provision here, then open VS Code attached to the container (reconnects, doesn't rebuild)
devcontainer up . && devcontainer open .

# Pause / tear down a workspace (great on a remote host); `up` restarts a stopped one
devcontainer stop .    # graceful stop, keep the container + data
devcontainer down .    # stop and remove it
```

Everything else — `up`, `build`, `exec`, `read-configuration`, `set-up`,
`run-user-commands`, `features` (info/package/publish/test/…), `templates`
(apply/publish/…), `outdated`, `upgrade` — mirrors the official CLI.

## Is it really compatible?

Compatibility is **validated**, not assumed. A pinned copy of the official
TypeScript CLI (`reference/`, currently **v0.88.0**) is the behavioral oracle: a
matrix of ~200 cases runs the *same* command through both CLIs and asserts the
outputs match (exit code, normalized stdout/stderr, and container/registry state).
Deliberate divergences (like the Go-only features above) are recorded, not hidden.

If you're evaluating a switch, the deliberate divergences are documented in
[`docs/DIVERGENCES.md`](docs/DIVERGENCES.md) and the full case matrix in
[`docs/parity/parity-matrix.yaml`](docs/parity/parity-matrix.yaml).

## Contributing / building

```sh
task build            # statically-linked ./devcontainer (CGO_ENABLED=0)
task test:unit        # hermetic Go unit tests
task test:integration # local HTTP integration tests
task lint             # linters
task build:cross      # static binaries for linux/{amd64,arm64}
```

Running the parity matrix (needs the submodule + Node + Docker):

```sh
git submodule update --init reference
task reference        # compile the TypeScript oracle
task parity:contract  # hermetic contract lane (no Docker)
task parity:runtime   # full matrix; creates real containers/images via Docker
```

- **[`docs/DIVERGENCES.md`](docs/DIVERGENCES.md)** — deliberate divergences, decisions & accepted limitations.
- **[`docs/parity/parity-matrix.yaml`](docs/parity/parity-matrix.yaml)** — the case matrix.

Releases are cut by pushing a `v`-prefixed CalVer tag (`vYYYYMMDD.NN`, e.g.
`v20260712.01` — matching `release.yml`'s `v*` trigger); GoReleaser then builds the
static binaries, SBOMs and the signed multi-arch image, published under
`spin-stack/devcontainer-cli`.

```
cmd/, internal/      the Go CLI implementation
docs/                user + migration docs
src/test/configs/    fixtures used by the parity matrix
reference/           git submodule → the official CLI @ v0.88.0 (parity oracle)
```
