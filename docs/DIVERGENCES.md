# Divergences, decisions & accepted limitations

This CLI is validated for behavioral parity with the reference TypeScript
`@devcontainers/cli` (pinned at **v0.89.0**, see [`parity/`](parity/)). Where it
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
  `build --secrets-file` (BuildKit build secrets; TS `build` has no such flag), `open`
  (launch VS Code attached to the workspace's dev container via a vscode-remote:// URI),
  `stop`/`down` (pause, or stop+remove, a workspace's container — Compose-aware; the
  upstream CLI leaves teardown to the editor), and the automatic credential bridge that
  hands the CLI's resolved auth to `docker build`.
- **A final `FROM` line continued with a backslash keeps its stage name.** TS
  `ensureDockerfileHasFinalStageName` reads only the physical line, so for
  `FROM \` followed by `alpine AS base` it writes `FROM \ AS dev`, a Dockerfile
  no builder accepts. This CLI asks BuildKit for that stage's name and leaves the
  Dockerfile as it is (`TestEnsureFinalStageNameReadsAContinuedFrom`,
  `internal/docker/testdata/ts-oracle/dockerfile.json`). Every other FROM line is
  handled exactly as TS handles it.
- **Compose builds may read the Features Dockerfile outside the build context.** Compose
  builds through bake, and bake on buildx 0.38 and later refuses to read a file outside the
  context unless the caller passes `--allow=fs.read`, which `docker compose build` cannot.
  The reference writes the Features Dockerfile under its cache folder and fails there with
  "additional privileges requested". This CLI starts its `docker compose` processes with
  `BUILDX_BAKE_ENTITLEMENTS_FS=0` unless the variable is already set
  (`TestWithBakeFSEntitlement`, `TestE2E_ComposeBuildLabel`).
- **A devcontainer.json that is not valid JSONC fails to load.** TS parses the configuration
  with jsonc-parser, whose error recovery keeps whatever it could read and reports nothing:
  `{bad` loads as `{}`, a missing comma or trailing garbage is skipped, and
  `{"image": , "remoteUser": "u"}` silently loses `image`. This CLI stops with the parse
  error, naming the file, line and column (`parse config <file>: hujson: line 1, column 2:
  ...`), because running a configuration with properties silently dropped is worse than
  refusing it. Valid JSONC (comments, trailing commas) loads as in TS
  (`TestLoadRejectsAConfigThatIsNotValidJSONC`).
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
- **OCI auth hardening is enforced through `oras-go`, which is stricter by default.**
  `--oci-auth-hardening` and `--allow-cross-origin-auth-host` behave as documented
  upstream (bearer realms pinned to the registry authority or a trusted auth host,
  token endpoints may not redirect), and `ociAuthDiagnostics` reports the same three
  flags in `up`/`build`/`read-configuration`. The difference is what happens
  **without** the flag: the reference CLI still forwards registry credentials to a
  challenge that arrives from another origin, while `oras-go` never does
  (GHSA-vh4v-2xq2-g5cg). Hardening off is therefore already safe here; the
  diagnostics still report what hardening *would* change, so the flag remains a
  faithful compatibility probe.
- **`BUILDKIT_INLINE_CACHE=1`** is omitted when `--cache-to` is an inline exporter
  (`/type\s*=\s*inline/i`), matching TS `isBuildxCacheToInline` — a parity fix over the
  earlier unconditional build-arg.
- **Two upstream fixes newer than the pinned reference are already ported.** A
  `${localEnv:VAR:default}` or `${containerEnv:VAR:default}` default keeps its colons
  (`my.registry.io:5000/img`; upstream #1213), where v0.89.0 keeps the text up to the next
  colon. A read-only mount keeps `:ro` in the Compose override (upstream #881), where
  v0.89.0's `convertMountToVolume` drops `readonly`. The `testdata/ts-oracle` goldens
  record both as divergence cases.
- **Dockerfile instructions are read with BuildKit's parser**, not with the reference's
  regular expressions, when resolving the base image and the `USER` of a Dockerfile.
  BuildKit unquotes `'...'` values, reads every `NAME=value` of one `ARG`/`ENV` line and
  takes `ENV A = B` as `A` = `= B`, as Docker does; the reference keeps the single quotes,
  reads only the first pair and takes `B`. Variable expansion, stage lookup and the final
  stage name otherwise follow the reference (recorded in
  `internal/{docker,cli}/testdata/ts-oracle/dockerfile.json`).
- **An unknown `${templateOption:NAME}` is always replaced with the empty string.** TS
  `replaceTemplatedValues` reads `options[NAME]` from a plain object, so a name that is not
  an option but an `Object.prototype` property (`constructor`, `toString`, …) becomes the
  inherited function's source text (`function Object() { [native code] }`) in the applied
  template. This CLI treats such a name like every other unknown option
  (`internal/templates/testdata/ts-oracle/templates.json`).
- **A `devcontainer-lock.json` that parses but is not shaped like a lockfile is an
  error.** The reference keeps whatever `JSON.parse` returns and looks each Feature up with
  `lockfile?.features[id]?.…`: an array, a number or a string throws a `TypeError` once a
  Feature is looked up, and a `features` value that is not an object pins nothing. This CLI
  refuses such a file when it reads it (`parse lockfile: …`). JSON that does not parse, or a
  file that cannot be read, fails with the reference's own message, and `null`, `false`, `0`
  and `""` are no lockfile, as in TS (`internal/features/testdata/ts-oracle/lockfile.json`).
- **The `devcontainer.metadata` label writes object values with their keys sorted.** The
  label is TS's `JSON.stringify` of the entries: each entry's properties in TS's pick
  order (`pickFeatureProperties` for a Feature, `pickConfigProperties` for the
  configuration) and `&`, `<`, `>` unescaped, as in TS. Within a property's object value
  (a mount object, `customizations`, `portsAttributes`, …) the keys are sorted, because a
  decoded Go map keeps no order, where TS keeps the order they were written in; an entry
  given in another order (a base image's label written by another tool) is likewise
  rewritten in pick order. The parsed metadata is the same; only the label's bytes differ
  (`internal/imagemeta/testdata/ts-oracle/imagemeta.json` records both cases).
- **Command results write nested objects with their keys sorted.** TS writes each result
  with `JSON.stringify`, so properties come in insertion order. Go matches that order for
  the results of `up`, `build`, `set-up`, `run-user-commands` and `read-configuration`,
  for `featuresConfiguration`, for the top level of `configuration` (the document's
  order, with `customizations` moved from deprecated properties and then `configFilePath`
  last) and for the top level of `mergedConfiguration` (the configuration without
  `mergeConfiguration`'s `replaceProperties`, then the properties it sets). Objects nested
  deeper are written with sorted keys, because a decoded Go map keeps no order: a
  configuration's `containerEnv` or `customizations`, merged objects such as `remoteEnv`
  or `portsAttributes`, and each Feature in `featuresConfiguration.featureSets`, which
  TS writes in its `devcontainer-feature.json` order. The parsed values are the same; the
  parity matrix's `key_order` cases hold the orders that match.

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
  `github.com/spin-stack/devcontainer-cli`), distroless/static, non-root. **Contract:
  it is a distribution/self-contained-operations artifact, not a batteries-included
  runner.** It carries only the static `/devcontainer` binary (plus CA certs), so it
  serves two uses: (1) extracting the binary onto a host, and (2) running the
  commands that work fully in-process — `read-configuration`, `features`/`templates`
  authoring, OCI push/pull, `--version`. The commands that shell out
  (`up`/`build`/`exec`/Compose) need the `docker`/`buildx`/`docker compose` binaries
  and a reachable daemon, which the distroless image deliberately does **not**
  bundle; run those on a host that has Docker, or `docker cp` the binary out. The
  release smoke test (`--version`) validates the distribution use, not the
  docker-dependent commands (which cannot run from this image by design).
- **Self-containment stance.** Container/engine operations, Docker-context resolution and
  git-root detection run in-process (Go libraries / stdlib), and lifecycle hooks run via
  the Docker exec API rather than `docker exec`. The following are kept as shell-outs on
  purpose: `docker buildx build` (buildx feature breadth + the user's builder/context; a
  library would regress buildx or pull the heavy buildkit client), `docker compose` (its
  output is not in the compared stream, so a library adds large deps for zero parity
  gain), interactive `docker exec -it`, and the credential-helper protocol (external
  executables by design). These shell-outs receive the command's `context.Context` (via
  `exec.CommandContext`), so a deadline/cancel — or `Ctrl-C` — aborts an in-flight
  `build`/`compose` subprocess rather than orphaning it.

## Accepted limitations

Known gaps that are deliberately not closed; each is a conscious trade-off, not an
oversight.

- **Which of several failing parallel lifecycle commands is named.** When more than one
  command of an object-form hook (`"postCreateCommand": {"a": ..., "b": ...}`) fails,
  TS names the first failure in the object's key order (`Promise.allSettled` +
  `find`); the command maps decode without key order here, so the first failure in
  sorted order is named. With one failing command — the common case — the description
  (`b of postCreateCommand from devcontainer.json failed.`) is identical. Keeping key
  order would mean an ordered representation for every lifecycle command through the
  config, metadata and merge; `TestHookErrorDescribesTheCommandAndItsOrigin` pins the
  current choice.

- **Byte-for-byte tarball parity is unattainable** because of `mtime` differences; tarball
  contents are compared by parsed structure, not raw bytes.
- **Cloud registry auth matrix is out of default CI scope.** The hermetic auth paths
  (401→bearer, credential-helper protocol, `DEVCONTAINERS_OCI_AUTH`, `GITHUB_TOKEN`) are
  unit-tested; a real ACR (identity/refresh) / ECR / authenticated-GHCR matrix is
  secrets-gated and non-blocking. Credential helpers are Linux-only
  (`secretservice`/`pass`).
- **TS→Go metadata interop** is exercised by `TestMetadataInteropTSToGo`, which builds
  the same configuration with the oracle and with this CLI and compares the merged
  `devcontainer.metadata` labels; it runs when the oracle is compiled (`task reference`)
  and Docker is available. The Go→Go round-trip and whitespace invariance are
  unit-tested.
- **Version banner** differs cosmetically: this CLI reports a git hash / CalVer, TS a
  semver, and the banner box width depends on the version length. Verbose commands
  (features-test / features-info) are compared via `exit_code` / stderr rather than the
  banner.
- **Legacy Feature fallback via GitHub Releases** is not implemented; feature resolution
  is OCI-first (the supported path for v2 features). A configuration that references one fails with the
  resolution error (read-configuration included) instead of silently omitting the Feature.
