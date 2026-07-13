# Use cases

## Existing development workspaces

For a repository that already contains `.devcontainer/devcontainer.json`:

```sh
devcontainer check
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . sh -lc './scripts/test'
```

Open the provisioned workspace in VS Code with:

```sh
devcontainer open --workspace-folder .
```

`open` hands the workspace to the Dev Containers extension using the same container
labels as `up`, allowing the editor to reconnect instead of provisioning a separate
container.

Use `devcontainer stop .` to preserve the container filesystem while releasing
runtime resources. Use `devcontainer down .` to remove the container. For Compose
configurations these commands operate on the Compose project.

## CI workflows

The static binary can be added to a Linux runner image without installing Node.js.
A basic workflow is:

```sh
set -eu

devcontainer check
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . sh -lc './scripts/ci'
devcontainer down --workspace-folder .
```

For cleanup that must run after a failed test, use the CI system's finalizer or trap
mechanism rather than relying only on the final command.

Build an image without starting a workspace when a pipeline only needs a prebuild:

```sh
devcontainer build \
  --workspace-folder . \
  --image-name ghcr.io/acme/project-dev:<tag> \
  --push
```

When a Dockerfile consumes private values through BuildKit secret mounts, keep them
out of layers and command-line arguments:

```sh
devcontainer build --workspace-folder . --secrets-file secrets.json
```

The secrets file contains a JSON object such as `{"NPM_TOKEN":"..."}`. Treat it as
sensitive and delete it using the CI system's secret-file lifecycle.

## Remote development platforms

A platform can treat `devcontainer.json` as the workspace contract while using this
CLI as the provisioner.

### Prebuild and reuse

1. Resolve the configuration and request its deterministic cache key:

   ```sh
   devcontainer read-configuration --workspace-folder . --cache-key
   ```

2. Look up an image associated with that key. If it does not exist, build and publish
   one with `devcontainer build`.
3. Start a workspace from the prebuilt image:

   ```sh
   devcontainer up \
     --workspace-folder . \
     --cache-image ghcr.io/acme/project-dev:sha-abc123
   ```

`--cache-image` skips the image build and Feature installation. It is intended for
image- or Dockerfile-based configurations and is not supported for Compose.

The key is hermetic and includes normalized configuration and relevant local build
inputs. Pin Feature references with digests or commit the Feature lockfile when the
key must identify exact Feature contents.

### Workspace lifecycle

```sh
devcontainer up .
devcontainer stop .   # keep the container and its state
devcontainer up .     # restart the same stopped container
devcontainer down .   # remove it
```

For repositories with multiple Dev Container configurations, pass `--config` so
lifecycle commands resolve the intended container.

### Host image validation

Run `devcontainer check --json` while qualifying a host image. The report makes
Docker daemon, buildx, cache-export, Compose, disk-space, and SELinux status
available to automation. Use `devcontainer setup --dry-run --json` to inspect safe
remediations before applying them.
