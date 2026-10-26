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

## Upgrading from an earlier release of this CLI

Releases that close a divergence from the official CLI can change behavior that
automation relied on. These changes match the official CLI, so a workflow that also
runs the official CLI is unaffected:

- `features package`, `features publish`, and `templates publish` require the
  `<target>` folder argument. Earlier releases defaulted it to the current folder;
  without it the command now exits 1 with `Not enough non-option arguments: got 0,
  need at least 1`. Pass the folder explicitly, for example
  `devcontainer features package . --output-folder output`.
- A configuration that references a legacy GitHub Releases Feature fails, in
  `read-configuration` too, instead of silently leaving the Feature out.
- `up` with `--id-label` and no workspace folder or configuration fails with
  `No dev container config and no workspace found.` instead of attaching to the
  matching container.
- Features are staged under `<tmp>/devcontainercli-<user>/container-features/`,
  one folder per build, and that folder is kept as the official CLI keeps it.
- When the workspace is a Git worktree and the configuration sets both
  `workspaceMount` and `workspaceFolder`, `up` no longer mounts the main
  repository's Git common directory, and `read-configuration` no longer reports
  `additionalMountString`, as in the official CLI v0.89.0. With only
  `workspaceMount` set, the common directory is mounted under
  `/workspaces/<main repository>/.git` rather than under the custom mount's
  target. Earlier releases carried an unreleased upstream change (#1261) instead.
  A configuration that needs Git inside the container this way should add the
  common directory to `mounts` itself.
- A `${containerEnv}` reference without a variable name fails `up`, `set-up`,
  `run-user-commands`, `exec` and `read-configuration` with exit 1 and
  `'${containerEnv}' in <file> can not be resolved because no environment
  variable name is given.`, as the official CLI does. Earlier releases reported
  success for most of them. Name the variable, as in `${containerEnv:PATH}`.
- `exec` reads the configuration `--config` or `--override-config` names, also
  with `--container-id` and no `--workspace-folder`, and exits 1 when it is
  missing (`Dev container config (<path>) not found.`) or cannot be loaded.
  Earlier releases ignored those flags without a workspace folder and ran the
  command despite a configuration error.
- `read-configuration` looks up the workspace's container (or the one
  `--id-label` names) as the official CLI does, and exits 1 with nothing on
  stdout when that lookup fails, including when no Docker engine is reachable.
  Earlier releases ignored the failure and printed the configuration. Run it
  where the Docker engine answers.
- A local Feature (`./x`) must live inside the workspace's `.devcontainer/`
  folder, as in the official CLI. `build`, `up`, `read-configuration` and
  `upgrade` now exit 1 with `ERR: Feature './x' could not be processed. ...` for
  `../x`, an absolute path, or `./x` next to a `.devcontainer.json` at the
  workspace root, where earlier releases resolved the folder next to the
  configuration. Move the Feature's folder under `.devcontainer/`.
- `build` on a Docker Compose configuration without `--image-name` reports
  `"imageName"` as the image's name, a string, as the official CLI does. Earlier
  releases wrapped it in an array. With `--image-name`, and for image and
  Dockerfile configurations, it stays an array.
- The text log on stderr follows the official CLI's two formats. With stdout
  redirected or piped, each line starts with its UTC time
  (`[2026-01-02T03:04:05.678Z] text`) and Stop lines appear at `--log-level debug`
  and `trace`. With stdout on a terminal, lines keep the elapsed `[N ms]` prefix,
  are colored and end in `\r\n`, and Stop lines appear only at `trace`. Earlier
  releases always wrote `[N ms] text` without color. A script that parses the log
  should read the timestamped form, or use `--log-format json`.
- `build` and `up` write the Feature lockfile by default whenever the configuration
  uses Features, as the official CLI does: `devcontainer-lock.json` next to
  `devcontainer.json` (`.devcontainer-lock.json` next to `.devcontainer.json`) is
  created when missing and rewritten when the resolved Features change. Earlier
  releases wrote it only with `--experimental-lockfile` or `--frozen-lockfile`, so
  the file now appears in projects that never had one. Commit it to pin the
  Features' versions and digests, or pass `--no-lockfile` to neither read nor write
  it. `--experimental-lockfile` is accepted but does nothing more and prints
  `Warning: --experimental-lockfile is deprecated. Lockfiles are now enabled by
  default.` on stderr; `--experimental-frozen-lockfile` prints a warning to use
  `--frozen-lockfile` instead.
- `upgrade` always rewrites the lockfile in its canonical form, even when the
  existing one differs only in formatting.
- `upgrade --log-level` accepts only `error`, `info`, `debug`, and `trace`. Any
  other value exits 1 with `Invalid value "<value>" for --log-level` instead of
  running at the default level.
- `upgrade --config` naming a file that does not exist reports
  `Dev container config (<path>) not found.` instead of the operating system's
  read error. The exit code stays 1.
- `upgrade` no longer logs `Lockfile updated.` on stderr after writing the
  lockfile, and starts its log with the version header once the configuration
  loads, as the official CLI does. Check the exit code to tell whether the
  lockfile was written.
- `upgrade` on a configuration without Features exits 1 with
  `Failed to update lockfile` and writes no lockfile, with or without
  `--dry-run`. Earlier releases wrote or printed `{"features":{}}` and exited 0.
  Skip `upgrade` for configurations that use no Features.
- `upgrade` resolves Features the way `build` does. A Feature whose manifest or
  content cannot be fetched fails the command (exit 1,
  `ERR: Feature '<id>' could not be processed. ...`) instead of being left out
  of the lockfile. The lockfile also lists the Features that other Features
  depend on, with each entry's `dependsOn`, so it matches the one `build` and the
  official CLI write. Make every Feature reachable, with registry credentials
  where needed, before running `upgrade`.
- `upgrade --feature <id> --target-version <version>` rewrites the Feature's
  key in `devcontainer.json` to `<id without version>:<version>`, even with
  `--dry-run`, before generating the lockfile. Earlier releases validated the
  two flags but otherwise ignored them. Run it on a clean working tree, or
  without the flags, when the configuration must stay unchanged.
- Only a registry whose host is `localhost` is reached over plain HTTP. Earlier
  releases also used plain HTTP for `127.0.0.1`, which is now addressed over HTTPS
  like any other host. Refer to a local plain-HTTP registry as `localhost:<port>`.

## Rollback

Keep the previous official CLI version available during rollout. Because project
configuration is unchanged, rollback consists of restoring the executable used by
automation. Do not begin relying on Go-only flags until the migration has been
accepted; the official CLI will not recognize them.
