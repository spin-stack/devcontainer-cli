# Migrating from `@devcontainers/cli`

This CLI is designed to replace the official CLI in supported Linux and Docker
workflows without changing `devcontainer.json`.

## Before migrating

Confirm that the workflow uses:

- Linux on amd64 or arm64.
- Docker Engine API v1.44 or newer.
- Docker Compose v2, if it uses a Compose configuration.
- OCI-based Dev Container Features rather than the legacy GitHub Releases fallback.

Windows, macOS, Podman, and Compose v1 are not migration targets.

## Replace the executable

Install a pinned release and replace invocations such as:

```sh
devcontainer up --workspace-folder .
```

The executable name and the main command and flag surface match the official CLI;
the migration normally changes how the executable is installed, not the command
line or project configuration.

If existing automation invokes a JavaScript entry point directly, replace:

```sh
node /path/to/devcontainer.js up --workspace-folder .
```

with:

```sh
/usr/local/bin/devcontainer up --workspace-folder .
```

## Validate incrementally

1. Run `devcontainer check` on the target host.
2. Run `read-configuration` against representative projects and compare the output
   consumed by automation.
3. Test `build` and `up` against image-, Dockerfile-, and Compose-based projects
   that the organization uses.
4. Test private base images, push targets, cache exporters, proxies, and custom CA
   certificates where applicable.
5. Pin the CLI version before rolling it out to every runner or host.

The repository validates the core command families against a pinned official CLI,
but an organization's registry, network, and host policies remain part of its own
acceptance test.

## Review intentional differences

Read [Divergences, decisions, and accepted limitations](DIVERGENCES.md) before a
production rollout. Differences most likely to affect automation include:

- `--override-config` deep-merges a partial override instead of replacing the base
  configuration wholesale.
- `--terminal-log-file` and `--log-file` capture the same combined, non-ANSI stream.
- Legacy Feature fallback through GitHub Releases is not implemented.
- Platform and runtime support is intentionally limited to Linux and Docker.

The Go-only commands and flags are additive. Existing workflows do not need to use
them, but [Go-only features](go-only-features.md) describes the operational benefits
available after migration.

## Rollback

Keep the previous official CLI version available during rollout. Because project
configuration is unchanged, rollback consists of restoring the executable used by
automation. Do not begin relying on Go-only flags until the migration has been
accepted; the official CLI will not recognize them.
