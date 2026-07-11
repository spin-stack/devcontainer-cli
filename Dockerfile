# Minimal OCI image for the devcontainer CLI (Go rewrite).
#
# The binary is built statically (CGO_ENABLED=0), so it runs on the
# distroless "static" base — which is essentially just a nonroot user plus
# the CA certificate bundle. Those CA certs are required: the CLI performs
# TLS to OCI registries (ghcr.io, mcr, etc.) when pulling Features/Templates.
#
# This Dockerfile expects the binary to already exist in the build context as
# ./devcontainer. GoReleaser's `dockers:` block builds the binary first and
# drops it into the context (see .goreleaser.yml); for a local build, run
#   CGO_ENABLED=0 go build -o devcontainer ./cmd/devcontainer
# before `docker build`.
FROM gcr.io/distroless/static:nonroot

# Version/revision are injected at build time. GoReleaser passes them via
# build_flag_templates; a local build can pass --build-arg VERSION=... .
ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="devcontainer-cli"
LABEL org.opencontainers.image.description="Dev Container CLI (Go rewrite)"
LABEL org.opencontainers.image.source="https://github.com/spin-stack/devcontainer-cli"
LABEL org.opencontainers.image.url="https://github.com/spin-stack/devcontainer-cli"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${REVISION}"

# Distroless already runs as the nonroot user (uid 65532); be explicit.
USER nonroot:nonroot

COPY devcontainer /devcontainer

ENTRYPOINT ["/devcontainer"]
