# Troubleshooting

Start with:

```sh
devcontainer check
```

It validates the Docker daemon, buildx, cache export, Compose v2, Docker storage
space, and SELinux configuration, and prints a remediation for each non-OK result.

## Docker daemon is unreachable

Verify that Docker is running and that the current user can access the selected
Docker context:

```sh
docker context show
docker info
```

The CLI uses the host's Docker configuration and context. Fix daemon access before
debugging the Dev Container configuration.

## Buildx or cache export is unavailable

Inspect the proposed host change:

```sh
devcontainer setup --dry-run
```

Then run `devcontainer setup` to create and select a cache-capable
`docker-container` builder when that is the safe available remediation. Missing
plugins or changes requiring root remain manual host-administration tasks.

## A Compose configuration does not start

This project supports `docker compose` v2, not the standalone `docker-compose` v1
command. Confirm it is available:

```sh
docker compose version
```

## A bind-mounted workspace is denied on SELinux

On an enforcing SELinux host, Docker may not relabel the workspace mount. The host
check reports this condition. Depending on the host policy, add an appropriate
SELinux mount label or configure the container with:

```json
{
  "runArgs": ["--security-opt", "label=disable"]
}
```

Choose the policy with the host administrator; disabling labeling has security
implications.

## A private image, push, or build cache is unauthorized

Confirm which registry is involved and that credentials are available through the
Docker configuration, its credential helper, `DEVCONTAINERS_OCI_AUTH`, or the
supported token environment. The CLI passes resolved credentials to the build
subprocess, but it cannot create credentials that are absent or lack the required
repository scope.

Test the same registry reference and identity outside the full workspace build to
separate authentication policy from Dev Container configuration.

## A TLS-intercepting proxy breaks registry requests

Make the proxy CA available through `NODE_EXTRA_CA_CERTS` or `SSL_CERT_FILE`, and
check `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`. Despite the historical environment
variable name, `NODE_EXTRA_CA_CERTS` is honored by this CLI for compatibility with
existing Dev Container environments.

## The OCI image cannot run `up` or `build`

The published image is a distroless distribution artifact. It does not include the
Docker, buildx, or Compose executables required by commands that shell out. Extract
the binary onto a Docker-capable host or copy it into a runner image that contains
those tools. See [Installation](installation.md#oci-image).

## Behavior differs from the official CLI

Check [Divergences, decisions, and accepted limitations](DIVERGENCES.md). When the
behavior is not documented there, reduce the report to a project configuration and
command that can be run through both CLIs; the parity matrix uses the same form of
comparison.
