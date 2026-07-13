# devcontainer

**Run Dev Containers anywhere you have Linux and Docker.**

`devcontainer` is a self-contained CLI for starting development environments from
[`devcontainer.json`](https://containers.dev) in CI, remote development hosts, and
internal developer platforms. It ships as a single static binary, requires no
Node.js runtime, and is behaviorally compatible with the official
`@devcontainers/cli` within its [supported scope](#supported-scope).

Use it to give developers and automation the same reproducible environment, add
Dev Container support to Linux hosts without managing Node.js, and build prebuild
workflows without reimplementing Dev Container behavior.

## Choose your path

- **I already have a Dev Container:** [install the binary](docs/installation.md),
  run `devcontainer up`, and follow the [migration guide](docs/migration.md) for
  compatibility details.
- **I want to use Dev Containers in CI:** see the
  [CI workflow](docs/use-cases.md#ci-workflows) for a minimal build-and-run example.
- **I am building a remote development platform:** see the
  [platform workflow](docs/use-cases.md#remote-development-platforms) for prebuilds,
  deterministic cache keys, lifecycle management, and editor hand-off.
- **I am evaluating compatibility:** read the
  [supported scope](#supported-scope) and [deliberate divergences](docs/DIVERGENCES.md).

## Why use this CLI?

| What you need | What `devcontainer` provides |
| --- | --- |
| A predictable artifact for hosts and runners | One static Linux binary with no Node.js runtime or `node_modules` |
| Existing Dev Container behavior | The core command surface is tested against a pinned official CLI |
| Fast, actionable host validation | `devcontainer check` diagnoses Docker, buildx, cache export, Compose, disk, and SELinux; `setup` applies safe fixes |
| Reusable prebuilds | A deterministic `--cache-key` and `up --cache-image` workflow |
| Private registry access in automated builds | Credentials resolved by the CLI are passed to `docker build` automatically |
| Safer image builds | BuildKit secrets through `build --secrets-file` |
| Workspace lifecycle outside an editor | `open`, `stop`, and `down`, including Docker Compose projects |

The official CLI remains the compatibility reference. This project focuses on
portable distribution and operational workflows around it.

## Quick start

### 1. Install

Download the archive for your architecture from the
[Releases page](https://github.com/spin-stack/devcontainer-cli/releases), extract
`devcontainer`, and place it on your `PATH`:

```sh
tar xzf devcontainer_<version>_linux_<amd64-or-arm64>.tar.gz
chmod +x devcontainer
sudo mv devcontainer /usr/local/bin/
devcontainer --version
```

See [Installation](docs/installation.md) for checksum verification, building from
source, prerequisites, and the OCI image's intended use.

### 2. Run an existing project

From a repository that contains `.devcontainer/devcontainer.json`:

```sh
devcontainer check
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . sh -lc 'printf "ready: %s\n" "$USER"'
```

`up` builds or pulls the configured image, installs Features, starts the container,
and mounts the workspace. `exec` runs a command in that environment.

If you do not have a Dev Container configuration yet, start with the
[Dev Container specification and templates](https://containers.dev).

## Common workflows

### Use the same environment in CI

```sh
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . sh -lc './scripts/test'
devcontainer down --workspace-folder .
```

### Open the workspace in VS Code

```sh
devcontainer up --workspace-folder .
devcontainer open --workspace-folder .
```

The editor reconnects to the provisioned container instead of creating a separate
one.

### Reuse a prebuilt environment

```sh
devcontainer read-configuration --workspace-folder . --cache-key
devcontainer up --workspace-folder . \
  --cache-image ghcr.io/acme/project-dev:sha-abc123
```

The cache key represents the resolved configuration and build inputs. A platform
can use it to select a prebuilt image without reproducing the CLI's hashing logic.
See [Use cases](docs/use-cases.md) for a complete workflow.

## Supported scope

This project supports and validates:

- Linux on amd64 and arm64.
- Docker Engine API v1.44 or newer (Docker 25+).
- Docker Compose v2 for Compose-based configurations.
- The core `up`, `build`, `exec`, `read-configuration`, `run-user-commands`,
  `features`, and `templates` command families.

Windows, macOS, Podman, Docker Compose v1, and legacy Feature fallback through
GitHub Releases are not supported targets. “Compatible” means compatibility within
this scope; it does not mean that every platform or historical upstream behavior is
implemented.

A pinned official TypeScript CLI (`reference/`, currently v0.88.0) is the behavioral
oracle. Roughly 200 cases run commands through both CLIs and compare exit status,
normalized output, and relevant container or registry state. See the
[parity matrix](docs/parity/parity-matrix.yaml) and
[documented divergences](docs/DIVERGENCES.md).

## Features unique to this implementation

```sh
devcontainer check
devcontainer setup
devcontainer open .
devcontainer stop .
devcontainer down .

devcontainer read-configuration --workspace-folder . --cache-key
devcontainer up --workspace-folder . --cache-image <image>
devcontainer build --workspace-folder . --secrets-file secrets.json
```

The [Go-only features reference](docs/go-only-features.md) documents their behavior,
flags, limitations, and automation-friendly JSON output.

## Documentation

- [Documentation overview](docs/README.md)
- [Installation and prerequisites](docs/installation.md)
- [Migrating from `@devcontainers/cli`](docs/migration.md)
- [CI and remote development platform workflows](docs/use-cases.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Go-only features](docs/go-only-features.md)
- [Divergences and accepted limitations](docs/DIVERGENCES.md)

## Contributing

The usual local development loop is:

```sh
task build
task test:unit
task test:integration
task lint
```

Parity and Docker-backed suites have additional prerequisites:

```sh
git submodule update --init reference
task reference
task parity:contract
task test:e2e
task parity:runtime
```

Coverage is kept separate by execution layer so a broad Docker or parity suite
cannot hide a regression in fast unit coverage. Run `task --list` for all development,
coverage, compliance, and release tasks.

Releases use `v`-prefixed CalVer tags (`vYYYYMMDD.NN`) and publish static archives,
checksums, SBOMs, and the signed multi-architecture image.

## License

See [LICENSE.txt](LICENSE.txt).
