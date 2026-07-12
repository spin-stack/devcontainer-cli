# Divergences, decisions & accepted limitations

This CLI is validated for behavioral parity with the reference TypeScript
`@devcontainers/cli` (pinned at **v0.88.0**, see [`migration/`](migration/)). Where it
deliberately differs, the difference is recorded here — this is the durable record of
*intentional* departures from the oracle, not a backlog. User-facing additions are
documented in [`go-only-features.md`](go-only-features.md).

## Deliberate divergences from the reference CLI

These are intentional behavior differences; each is covered by tests and, where it
touches a compared surface, reflected in the parity matrix.

- **Go-only commands / flags** (full reference in [`go-only-features.md`](go-only-features.md)):
  `check` and `setup` (host preflight/remediation), `up --cache-image` (boot from a
  prebuilt image, skip build + feature install), `read-configuration --cache-key`
  (deterministic content hash; additive — default output is byte-identical to TS),
  `build --secrets-file` (BuildKit build secrets; TS `build` has no such flag), and the
  automatic credential bridge that hands the CLI's resolved auth to `docker build`.
- **`--override-config` deep-merges** the override onto the base config, whereas TS
  replaces the config wholesale (`readDocument(overrideConfigFile ?? configFile)`). With
  no readable base, the override stands alone — identical to TS. This lets an orchestrator
  pass a partial override. Only matrix case for the flag is the error path, so contract +
  semantic stay green.
- **`--terminal-log-file` tees the same combined stream** as `--log-file`. TS produces two
  files (a terminal stream with ANSI and a plain one); this CLI keeps a single log stream
  (no self-managed PTY — see decisions), so both flags capture the same output (without
  ANSI). Never a black hole.
- **`config.build.cacheFrom`** is honored (wired to `--cache-from` after the flag's
  values) — matching `singleContainer.ts`. Upstream defines the field; this is a parity
  fix, noted here because it was previously a dead field.
- **`BUILDKIT_INLINE_CACHE=1`** is omitted when `--cache-to` is an inline exporter
  (`/type\s*=\s*inline/i`), matching TS `isBuildxCacheToInline` — a parity fix over the
  earlier unconditional build-arg.

## Firm decisions & scope

- **Platform: Linux only** (amd64 / arm64). Windows and macOS are not targets — no
  runtime/E2E/release/`windows-latest`/ConPTY lane. The `platform="win32"` logic is kept
  solely for parity with the oracle. (arm64 runtime is validated via a non-gating,
  QEMU-emulated experimental job.)
- **Runtime: Docker only.** Podman is not supported (no parity guarantee or test).
- **Compose: v2 only** (`docker compose`). Compose v1 (`docker-compose`) is not supported.
- **`exec`: inherited terminal** (`docker exec -it` inherits the controlling terminal),
  no self-managed PTY. The 128+N contract comes from the child process. Interactive
  `docker exec` is deliberately kept as a shell-out.
- **Docker Go SDK: `github.com/moby/moby/{client,api}`** (the v29 "options-in,
  result-out" surface), replacing the deprecated `github.com/docker/docker`. The
  top-level `github.com/moby/moby` (v2) module is an internal implementation detail and is
  deliberately **not** a dependency. Requires Docker Engine API ≥ v1.44 (Docker v25+).
- **OCI image: `ghcr.io/spin-stack/devcontainer-cli`** (source repo
  `github.com/spin-stack/devcontainer-cli`), distroless/static, non-root.
- **Self-containment stance.** Container/engine operations, Docker-context resolution and
  git-root detection run in-process (Go libraries / stdlib), and lifecycle hooks run via
  the Docker exec API rather than `docker exec`. The following are kept as shell-outs on
  purpose: `docker buildx build` (buildx feature breadth + the user's builder/context; a
  library would regress buildx or pull the heavy buildkit client), `docker compose` (its
  output is not in the compared stream, so a library adds large deps for zero parity
  gain), interactive `docker exec -it`, and the credential-helper protocol (external
  executables by design).

## Accepted limitations

Known gaps that are deliberately not closed; each is a conscious trade-off, not an
oversight.

- **Programmatic context cancellation of build/compose subprocesses is not wired.** The
  `docker build`/`docker compose` shell-outs run under `context.Background()`, so a
  ctx deadline/cancel does not abort an in-flight subprocess. Interactive `Ctrl-C` still
  aborts it (SIGINT reaches the child via the shared process group). Wiring the command
  `ctx` through the runner is a possible future refinement.
- **Byte-for-byte tarball parity is unattainable** because of `mtime` differences; tarball
  contents are compared by parsed structure, not raw bytes.
- **Cloud registry auth matrix is out of default CI scope.** The hermetic auth paths
  (401→bearer, credential-helper protocol, `DEVCONTAINERS_OCI_AUTH`, `GITHUB_TOKEN`) are
  unit-tested; a real ACR (identity/refresh) / ECR / authenticated-GHCR matrix is
  secrets-gated and non-blocking. Credential helpers are Linux-only
  (`secretservice`/`pass`).
- **TS→Go metadata interop** (build with the TS oracle, read with Go) is skip-guarded in
  the hermetic unit tests (which do not compile the oracle); it is exercised end-to-end by
  the runtime lane's metadata cases (`container-metadata-success`,
  `read-configuration.features-configuration`). The Go→Go round-trip and whitespace
  invariance are unit-tested.
- **Version banner** differs cosmetically: this CLI reports a git hash / CalVer, TS a
  semver, and the banner box width depends on the version length. Verbose commands
  (features-test / features-info) are compared via `exit_code` / stderr rather than the
  banner.
- **Legacy Feature fallback via GitHub Releases** is not implemented; feature resolution
  is OCI-first (the supported path for v2 features).
