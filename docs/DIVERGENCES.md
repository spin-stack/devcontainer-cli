# Divergences, decisions & accepted limitations

This CLI is validated for behavioral parity with the reference TypeScript
`@devcontainers/cli` (pinned at **v0.90.0**, see [`parity/`](parity/)). Where it
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
  `build --secrets-file` (BuildKit build secrets; TS `build` has no such flag),
  `--ca-bundle` on `build` and `up` (extra CAs trusted in Feature
  installs, opted-in Dockerfile `RUN`s and the container, mounted and never copied), `open`
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
- **`GITHUB_TOKEN` is sent only to GitHub's own hosts when a Feature is a tarball URL.**
  TS `getRequestHeaders` adds `Authorization: Bearer $GITHUB_TOKEN` to any direct-tarball
  URI that starts with `https://github.com` or `https://api.github.com`, so it also hands
  the token to `https://github.com.attacker.example/…`, `https://github.com@attacker.example/…`
  (userinfo) and `https://api.github.com.evil/…`: anyone who can put a Feature URL in a
  devcontainer.json, or in a Feature's `dependsOn`, can collect it. This CLI sends the
  token, and logs `Using environment GITHUB_TOKEN.` / `No environment GITHUB_TOKEN
  available.`, only when the URL parses, its scheme is `https`, it has no userinfo and its
  host is exactly `github.com` or `api.github.com`; for any other host it sends no token
  and logs neither line. Matching ignores case and one trailing dot, and allows any port,
  as TS does (TS normalizes the id with `new URL(...)`, which lowercases the host): these
  still name GitHub's host, and TLS verification binds the token to GitHub's certificate.
  No parity case can compare this with TS without leaking a token, so unit tests pin it:
  `TestIsGitHubTarballURI`, `TestFeatureRequestHeadersSendTheTokenOnlyToGitHub` (header and
  log lines for real and look-alike hosts) and
  `TestDownloadFeatureTarballKeepsTheTokenFromALookAlikeURL` (a TLS server reached through
  `https://github.com@<server>/…` receives no token).
- **`BUILDKIT_INLINE_CACHE=1`** is omitted when `--cache-to` is an inline exporter
  (`/type\s*=\s*inline/i`), matching TS `isBuildxCacheToInline` — a parity fix over the
  earlier unconditional build-arg.
- **Two upstream fixes newer than the pinned reference are already ported.** A
  `${localEnv:VAR:default}` or `${containerEnv:VAR:default}` default keeps its colons
  (`my.registry.io:5000/img`; upstream #1213), where v0.90.0 keeps the text up to the next
  colon. A read-only mount keeps `:ro` in the Compose override (upstream #881), where
  v0.90.0's `convertMountToVolume` drops `readonly`. The `testdata/ts-oracle` goldens
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

The entries below fix upstream bugs that are still present upstream; each names the
upstream issue it fixes.

- **GPU detection also asks `nvidia-container-cli`.** With `--gpu-availability detect`
  (the default), TS `checkDockerSupportForGPU` enables the GPU whenever `docker info`
  lists the `nvidia` runtime. The runtime is often installed on machines without a GPU
  (cloud images carry it), and `up` then fails starting a container with `--gpus all`
  (upstream #319). This CLI also requires `nvidia-container-cli info` to succeed when
  that binary is on `PATH`; without the binary it decides from `docker info` alone, as TS
  does (`TestCheckGPUAvailability_NvidiaCLIProbe`, `internal/cli/upstream_fixes_test.go`).
- **`build [path]` builds the workspace at `path`.** TS declares the positional
  `build [path]` but `doBuild` never reads it, so `devcontainer build ./project` builds
  the current folder (upstream #8). This CLI takes the path as the workspace folder when
  `--workspace-folder` is not given; the flag wins when both are
  (`TestBuildPositionalPathIsTheWorkspace`, `internal/cli/build_repo_held_test.go`).
- **A Dockerfile's own `<Dockerfile>.dockerignore` applies when the CLI builds a
  Dockerfile it generated.** BuildKit reads the ignore file next to the Dockerfile it is
  given. TS builds a Dockerfile it writes in a folder of its own (to add Features and the
  metadata label, also for a Compose service), so the user's
  `.devcontainer/Dockerfile.dockerignore` is not read and the files it excludes are sent
  in the build context (upstream #969). This CLI copies the ignore file next to every
  Dockerfile it generates (`TestCopyDockerignore`,
  `TestBuildAndExtendComposeDockerfileService` in `internal/cli/compose_up_held_test.go`,
  and `TestE2E_SiblingDockerignore` in `internal/cli/e2e_fixes_test.go`).
- **A `LABEL devcontainer.metadata` in the Dockerfile is kept.** TS builds the image's
  metadata label from the base image's label and the configuration and writes it over the
  image, so a `LABEL devcontainer.metadata=…` written in the built stage of the user's
  Dockerfile is lost (upstream #1225). This CLI merges that stage's entries between the
  base image's and the configuration's (`TestStageLabelsDevcontainerMetadata` and
  `TestStageLabelsReadsTheNamedStage` in `internal/docker`, `TestDockerfileImageMetadata`
  in `internal/cli/build_held_test.go`).
- **`build --label` applies to a Docker Compose configuration.** `docker compose build`
  takes no labels, and TS drops `--label` for a Compose configuration without a word
  (upstream #930). This CLI applies the labels with one more build, `FROM <image>`, tagged
  with the `--image-name` values or, without them, the image's own name
  (`TestE2E_ComposeBuildLabel`, `internal/cli/e2e_fixes_test.go`).
- **`features generate-docs` and `templates generate-docs` document the folders they are
  given.** TS takes every entry of the project folder as an item folder: pointed at a
  single Feature or Template folder it looks for metadata under each of its files and
  documents nothing, and each top-level file (a `README.md`, a `LICENSE`) is reported with
  a `not found … Skipping...` warning (upstream #876). This CLI documents the folder itself
  when it holds `devcontainer-<feature|template>.json`, and otherwise reads only its
  sub-folders, skipping files without a word (`TestGenerateDocs_SingleFeatureFolder`,
  `TestGenerateDocs_IgnoresTopLevelFiles`, `internal/cli/collection_commands_test.go`).
  The metadata is also read strictly: TS's `jsonc.parse` recovers from syntax errors and
  documents whatever it could read, while this CLI logs `Failed to parse <file>` and skips
  an item whose metadata is not valid JSONC; comments and trailing commas are accepted as
  in TS (`TestGenerateDocs_SkipsMetadataThatIsNotValidJSONC`), for the same reason a
  devcontainer.json that is not valid JSONC fails to load.
- **Host variables in a Feature's `containerEnv` are resolved in the image's `ENV`
  lines.** TS writes a Feature's `containerEnv` into the Features Dockerfile as given, so
  `"containerEnv": {"CACHE": "${localEnv:HOME}/.cache"}` becomes
  `ENV CACHE="${localEnv:HOME}/.cache"`, which BuildKit rejects (`unsupported modifier
  (:H) in substitution`) (upstream #308). This CLI resolves `${localEnv:…}`,
  `${localWorkspaceFolder}` and the other host variables before it writes the `ENV` line,
  as it does for the configuration's own `containerEnv`
  (`TestExtendImageWithFeaturesResolvesFeatureHostVars` in
  `internal/cli/feature_install_nodocker_held_test.go`, `TestComposeFeatureSets` in
  `internal/cli/compose_up_held_test.go`).
- **The Linux default credential helper is `secretservice`.** When the Docker config
  has no `auths`, `credHelpers` or `credsStore` for any registry, TS falls back to
  `docker-credential-pass` when `pass` is on `PATH` and otherwise to
  `docker-credential-secret`, a binary that does not exist, so the fallback never finds a
  credential. This CLI falls back to `docker-credential-secretservice`, the
  libsecret helper; the `pass` choice is unchanged (`TestPlatformDefaultHelperFollowsPass`,
  `TestDockerConfigWithAuthSkipsPlatformDefault` in `internal/oci/auth_held_test.go`,
  `TestDefaultCredentialHelperName` in `internal/oci/auth_helper_test.go`).
- **A credential helper that does not answer within 15 seconds is skipped.** TS waits
  for a helper for as long as it runs, so a helper blocked on a locked keyring or an
  unanswered prompt hangs the command. This CLI stops the helper after 15 seconds, warns
  (`Credential helper "<name>" timed out after 15s for "<registry>"; skipping it …`) and
  goes on without that credential (`GITHUB_TOKEN` or anonymous access)
  (`TestCredentialHelperThatHangsTimesOut`, `internal/oci/auth_held_test.go`).
- **`up --remove-existing-container` removes the container first.** TS removes the
  existing container only when it looks for it to reuse, after the disallowed-Features
  check and after `initializeCommand` has run, so a failing `initializeCommand` or a
  disallowed Feature leaves the old container in place although the flag asked for it to
  go (upstream #844). This CLI removes it before both
  (`TestE2E_RemoveExistingBeforeInitialize`, `internal/cli/e2e_fixes_test.go`).
- **`updateRemoteUserUID` changes the UID of a user without a home folder.** The TS
  `updateUID.Dockerfile` runs `chown -R $NEW_UID:$NEW_GID $HOME_FOLDER` unconditionally
  and unquoted, so a remote user whose home folder does not exist (a system user created
  with `--no-create-home`) fails the image update, and a home path with spaces is split
  (upstream #109). This CLI's Dockerfile chowns the home folder only when it exists, and
  quotes it (`TestE2E_UpdateUIDSystemUser`, `internal/cli/e2e_fixes_test.go`).

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
  `build`/`compose` subprocess rather than orphaning it. One consequence is visible in
  the log: TS runs every engine operation through the `docker` CLI (`plainExec`) and logs
  each one as a `Start: Run: docker …` / `Stop (N ms): Run: docker …` pair (`start` and
  `stop` records with `--log-format json`). An operation this CLI performs through the
  Docker Engine SDK (inspecting, listing, creating, starting and removing containers,
  lifecycle hooks) writes no such pair; the shell-outs above log a `Run: …` line.

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

- **A lone surrogate in a string is written as U+FFFD.** Command results are written as
  `JSON.stringify` writes them: `&`, `<`, `>`, U+2028 and U+2029 as they are, not
  escaped as `encoding/json` would (`read-configuration.json-stringify-escapes-success`).
  A configuration string holding a lone UTF-16 surrogate escape (`"\ud800"`) is the
  exception: a Go string cannot carry one, so the JSON decoder replaces it with U+FFFD
  when the file is read, and the result holds that character where TS writes the
  escape back.
- **Byte-for-byte tarball parity is unattainable** because of `mtime` differences; tarball
  contents are compared by parsed structure, not raw bytes.
- **Cloud registry auth matrix is out of default CI scope.** The hermetic auth paths
  (401→bearer, credential-helper protocol, `DEVCONTAINERS_OCI_AUTH`, `GITHUB_TOKEN`) are
  unit-tested; a real ACR (identity/refresh) / ECR / authenticated-GHCR matrix is
  secrets-gated and non-blocking. The platform default credential helper, used when the
  Docker config names none, is the Linux one only (`pass`, else `secretservice`; see
  *The Linux default credential helper is `secretservice`* above).
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
- **Mermaid node ids differ from TS.** `features info dependencies` and
  `features resolve-dependencies` print the dependency graph as a Mermaid flowchart. TS
  names each node with the first six hex digits of the SHA-256 of `JSON.stringify` of the
  whole graph node (`generateMermaidNode`), which includes the resolved Feature set and
  its metadata and the node's `dependsOn` / `installsAfter` lists, in the order a
  JavaScript object keeps. This CLI hashes the node's type, user Feature id, options,
  round priority and aliases instead, since reproducing TS's bytes would mean
  reproducing that whole object in insertion order. The graph's nodes, edges and labels
  are the same and each edge points at its node's own id; the parity harness replaces
  the ids with `<NODE>` (`reMermaidNode` in `normalizeStringOpts`,
  `internal/cli/parity_matrix_test.go`) for `features-info.dependencies-text` and
  `features.resolve-dependencies-success`.
- **A Feature or Template reference whose digest fails validation is refused.** For
  `<ref>@sha256:<hex>` where `<hex>` does not match the version pattern (empty, or
  starting with `-`), TS `getRef` logs `Digest for input '…' failed validation` and still
  returns the reference, where its other checks (the digest's form and algorithm, the
  tag) return no reference. This CLI refuses the reference (`digest "…" does not match expected
  pattern`), because accepting it would weaken validation; a valid digest behaves as in
  TS. The `internal/oci/testdata/ts-oracle/ref.json` golden records both cases
  (`ghcr.io/x/y/z@sha256:`, `ghcr.io/x/y/z@sha256:-bad`), checked by `TestTSOracleRef`.
