# Go-only features

These commands and flags are **exclusive to this Go CLI** — they are not part of the
official `@devcontainers/cli`. Everything else mirrors the upstream behavior (see the
[parity docs](parity/)); the items here are deliberate additions or documented
divergences, each covered by tests.

- [`devcontainer check`](#devcontainer-check) — host preflight
- [`devcontainer setup`](#devcontainer-setup) — apply safe host fixes
- [`devcontainer open`](#devcontainer-open) — launch VS Code attached to the dev container
- [`up --cache-image`](#up---cache-image) — boot from a prebuilt image
- [`read-configuration --cache-key`](#read-configuration---cache-key) — deterministic cache key
- [`build --secrets-file`](#build---secrets-file) — BuildKit build secrets
- [Automatic credential bridge](#automatic-credential-bridge) — private registries in `docker build`
- [`--override-config` deep-merge](#--override-config-deep-merge) — partial overrides
- [`config.build.cacheFrom`](#configbuildcachefrom) — honored from devcontainer.json

---

## `devcontainer check`

Preflights the local Docker/BuildKit environment so you find problems *before* a build
fails cryptically halfway through.

```sh
devcontainer check          # human-readable table
devcontainer check --json   # machine-readable report
```

It probes:

| Check | Severity | Meaning |
| --- | --- | --- |
| `docker-daemon` | **fail** | The Docker daemon is reachable. Nothing works without it. |
| `buildx` | warn | The buildx plugin is installed (needed for modern builds). |
| `build-cache-export` | warn | The active builder can export build cache (`--cache-to` / `--output` / cross-`--platform`). *Fixable by `setup`.* |
| `compose-v2` | warn | `docker compose` (v2) is available (only needed for Compose-based configs). |
| `disk-space` | warn | The Docker data directory has enough free space. |
| `selinux` | warn | SELinux is **not** enforcing. When it is, Docker doesn't relabel bind mounts, so the workspace mount fails with a cryptic "Permission denied" until you add `"runArgs": ["--security-opt", "label=disable"]` (or `:z`/`:Z`). |

Each non-OK check prints a remediation hint. The command exits non-zero if a **fail**
check fails, and it **persists** the result to a state file:

```
$XDG_STATE_HOME/devcontainer/check.json     # or ~/.local/state/devcontainer/check.json
```

`up` and `build` read that state file and, **only on an interactive terminal**, print a
one-line, non-blocking hint if the host was never checked or a previous check found a
failing configuration. This never perturbs scripted/captured output.

Flags: `--json`, `--docker-path`.

## `devcontainer setup`

Runs the same diagnostics as `check` and **applies the fixes it safely can** without
`sudo` — currently, creating and selecting a `docker-container` buildx builder so
build-cache export / `--output` / cross-platform builds work. Anything that needs a
package manager or root is reported as a manual step.

```sh
devcontainer setup             # apply safe fixes
devcontainer setup --dry-run   # show what would change, change nothing
devcontainer setup --json
```

> Not to be confused with `set-up` (hyphenated), the upstream command that configures an
> *existing container* as a dev container. `setup` (one word) configures the **host**.

Flags: `--json`, `--dry-run`, `--docker-path`.

## `devcontainer open`

Open a workspace folder in VS Code **inside its dev container**, from the shell. It builds
the `vscode-remote://dev-container+…` folder URI for the workspace and launches the editor
with it — VS Code's Dev Containers extension then provisions (or reconnects to) the
container.

```sh
devcontainer up   --workspace-folder .    # provision (optional; VS Code will too)
devcontainer open --workspace-folder .    # attach the editor
# or just:
devcontainer open .
```

Because `up` and `open` stamp/consume the same `devcontainer.local_folder` /
`devcontainer.config_file` labels, running `up` first means VS Code **reconnects** to the
container you already provisioned instead of building a new one; running `open` alone lets
VS Code provision on connect. This is the bridge for using this CLI as the provisioner while
still editing in VS Code (the extension bundles its own CLI and cannot be pointed at an
external binary, so this URI hand-off is the supported path).

The remote authority is `dev-container+<hex>`, where `<hex>` is the hex-encoded JSON
`{"hostPath": <local folder>, "configFile": {"scheme": "file", "path": <devcontainer.json>}}`,
followed by the container's `workspaceFolder`. Use `--dry-run` to print the URI and launch
command without opening anything (handy for scripting or debugging).

Flags: `--workspace-folder`, `--config`, `--editor` (default `code`; e.g. `code-insiders`,
`cursor`), `--dry-run`.

## `up --cache-image`

Boot the container from an already-built image (features baked in), **skipping the image
build and feature installation entirely**. The merged configuration is recovered from the
image's `devcontainer.metadata` label — exactly as an image-based config would — while
`remoteUser`, mounts and lifecycle hooks still come from `devcontainer.json`.

```sh
devcontainer up --workspace-folder . \
  --cache-image ghcr.io/acme/app-devcontainer:sha-abc123
```

Ideal for prebuild reuse: an orchestrator builds the image once (tagged by a
[`--cache-key`](#read-configuration---cache-key)) and every consumer boots from it
instantly. Not supported with Compose configurations.

## `read-configuration --cache-key`

Emits a **deterministic, content-addressed cache key** for a resolved configuration, so
external orchestrators can implement prebuild reuse without re-implementing the hashing.

```sh
devcontainer read-configuration --workspace-folder . --cache-key
# → { "configuration": { … }, "cacheKey": "sha256:…" }
```

The key is a `sha256` over the normalized `devcontainer.json`, the `Dockerfile` (when
Dockerfile-based), the build context path, the features **lockfile** (which pins resolved
digests, when committed) and the proxy environment (`HTTP(S)_PROXY`, `NO_PROXY`, …). It is
**hermetic** — no network, no registry resolution — so identical inputs yield the same key
on any host.

The flag is **additive**: without it, the output is byte-for-byte identical to the upstream
CLI; with it, a `cacheKey` field is added.

> Pin feature refs (via `@sha256` or a committed lockfile) for the key to track the exact
> feature bits — unpinned refs hash by tag.

## `build --secrets-file`

Pass **BuildKit build secrets** to `devcontainer build`, so a `Dockerfile` can
`RUN --mount=type=secret,id=KEY …` without baking the value into a layer or exposing it on
the command line.

```sh
echo '{"NPM_TOKEN":"…","GH_TOKEN":"…"}' > secrets.json
devcontainer build --workspace-folder . --secrets-file secrets.json
```

Each `KEY` becomes `--secret id=KEY,env=KEY`; the value is delivered through the build
subprocess environment, never as an argument. Requires buildx (ignored with a warning on
the legacy builder).

## Automatic credential bridge

Not a flag — a behavior. The upstream CLI's credential chain applies only to its *own*
registry operations, so pulling a **private base image**, `--push`, or `--cache-to` to a
private registry forces you to `docker login` by hand, sometimes in several places.

This CLI resolves credentials for every registry a build touches (base `FROM`, push tags,
cache refs) using its full chain — `DEVCONTAINERS_OCI_AUTH`, the Docker config / credential
helpers, `GITHUB_TOKEN` — and hands them to the `docker build` subprocess via a temporary,
self-contained `DOCKER_CONFIG`. If no credentials resolve, nothing changes: a build that
already authenticated via the environment behaves exactly as before.

## `--override-config` deep-merge

**Documented divergence.** The upstream CLI *replaces* the configuration wholesale with the
`--override-config` file. This CLI **deep-merges** the override onto the base config (nested
objects merged recursively; scalars and arrays replaced), so an orchestrator can supply a
*partial* override without restating the whole `devcontainer.json`.

```sh
# base .devcontainer/devcontainer.json has image + features;
# override.json only needs to change remoteUser:
devcontainer up --workspace-folder . --override-config override.json
```

When there is no readable base config, the override stands alone — identical to the
upstream/replace behavior.

## `config.build.cacheFrom`

The `build.cacheFrom` property in `devcontainer.json` is honored (upstream defines the field
but this CLI now wires it through to `--cache-from`, after the values from the
`--cache-from` flag). No extra flag — just set it in your config:

```json
{ "build": { "dockerfile": "Dockerfile", "cacheFrom": "ghcr.io/acme/app:cache" } }
```
