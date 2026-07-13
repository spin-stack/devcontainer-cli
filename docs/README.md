# Documentation

This documentation is organized by the outcome you are trying to achieve.

## Start here

- [Installation](installation.md) — prerequisites, release archives, source builds,
  and the OCI distribution image.
- [Migration](migration.md) — replace `@devcontainers/cli` in an existing Linux and
  Docker workflow.
- [Use cases](use-cases.md) — local use, CI, and remote development platforms.
- [Troubleshooting](troubleshooting.md) — diagnose common host, build, registry, and
  workspace problems.

## Reference

- [Go-only features](go-only-features.md) — commands and flags added by this
  implementation.
- [Divergences, decisions, and accepted limitations](DIVERGENCES.md) — intentional
  differences from the official CLI and firm scope decisions.
- [Parity matrix](parity/parity-matrix.yaml) — machine-readable compatibility cases.
- [CLI flag inventory](parity/cli-flags-inventory.yaml) — tracked upstream command
  surface.

## Supported environment

The validated runtime is Linux (amd64 or arm64), Docker Engine API v1.44 or newer,
and Docker Compose v2 when a project uses Compose. Windows, macOS, Podman, and
Compose v1 are outside the supported scope.

The CLI is intended for individual workstations, CI runners, remote development
hosts, and services that provision Dev Containers. The container image is a
distribution artifact, not a Docker-in-Docker runner; see
[Installation](installation.md#oci-image).
