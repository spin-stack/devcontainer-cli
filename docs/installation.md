# Installation

## Prerequisites

Before running container operations, the host needs:

- Linux on amd64 or arm64.
- Docker Engine API v1.44 or newer (Docker 25+), with a reachable daemon.
- Docker buildx for modern image builds.
- Docker Compose v2 when `devcontainer.json` references Compose files.

After installing the CLI, run `devcontainer check`. It reports missing or
incompatible host capabilities before a long build reaches them.

## Release archive

Download the archive and `checksums.txt` for the desired version from the
[GitHub Releases page](https://github.com/spin-stack/devcontainer-cli/releases).

```sh
# Replace VERSION and ARCH (amd64 or arm64) with the release you selected.
VERSION=<version>
ARCH=amd64

curl -LO "https://github.com/spin-stack/devcontainer-cli/releases/download/v${VERSION}/devcontainer_${VERSION}_linux_${ARCH}.tar.gz"
curl -LO "https://github.com/spin-stack/devcontainer-cli/releases/download/v${VERSION}/checksums.txt"
sha256sum --check checksums.txt --ignore-missing

tar xzf "devcontainer_${VERSION}_linux_${ARCH}.tar.gz"
chmod +x devcontainer
sudo mv devcontainer /usr/local/bin/
devcontainer --version
```

Pin a specific release in CI and platform images instead of downloading `latest` at
runtime. A pinned artifact makes rollbacks and compatibility investigations
repeatable.

## Build from source

Building requires Go and, when using the project task, [Task](https://taskfile.dev).

```sh
git clone https://github.com/spin-stack/devcontainer-cli
cd devcontainer-cli
task build
# Equivalent minimal build:
# CGO_ENABLED=0 go build -o devcontainer ./cmd/devcontainer
```

The result is `./devcontainer`. Move it to a directory on `PATH` or copy it into the
host or runner image that will invoke it.

## Verify the host

```sh
devcontainer check
devcontainer check --json
```

The human-readable form includes remediation hints. The JSON form is suitable for
host-image validation and provisioning pipelines. `devcontainer setup --dry-run`
shows the safe fixes that the CLI could apply; run `devcontainer setup` to apply
them.

## OCI image

Releases also publish `ghcr.io/spin-stack/devcontainer-cli`. The image is
distroless, non-root, and contains the static `/devcontainer` binary plus CA
certificates.

It is useful for:

- Extracting the binary into another image or onto a host.
- Running commands implemented fully in-process, such as `--version`,
  `read-configuration`, and Feature or Template authoring and registry operations.

It is **not** a batteries-included runner for `up`, `build`, `exec`, or Compose.
Those commands require Docker, buildx, or Compose executables and a reachable Docker
daemon, which the distroless image intentionally does not bundle.

For example, extract the binary from the image:

```sh
container_id=$(docker create ghcr.io/spin-stack/devcontainer-cli:<version>)
docker cp "${container_id}:/devcontainer" ./devcontainer
docker rm "${container_id}"
```
