# Minimal OCI image for the devcontainer CLI (Go rewrite).
#
# The binary is built statically (CGO_ENABLED=0), so it runs on the
# distroless "static" base — which is essentially just a nonroot user plus
# the CA certificate bundle. Those CA certs are required: the CLI performs
# TLS to OCI registries (ghcr.io, mcr, etc.) when pulling Features/Templates.
#
# GoReleaser's `dockers_v2:` block does a single multi-platform buildx build and
# stages the per-platform binary under ${TARGETPLATFORM}/devcontainer in the build
# context. For a local single-arch build:
#   mkdir -p linux/amd64
#   CGO_ENABLED=0 go build -o linux/amd64/devcontainer ./cmd/devcontainer
#   docker buildx build --platform linux/amd64 --load -t devcontainer-cli .
FROM gcr.io/distroless/static:nonroot

# Version/revision are injected at build time. GoReleaser passes them via
# build_args; a local build can pass --build-arg VERSION=... .
ARG VERSION=dev
ARG REVISION=unknown
ARG TARGETPLATFORM

LABEL org.opencontainers.image.title="devcontainer-cli"
LABEL org.opencontainers.image.description="Dev Container CLI (Go rewrite)"
LABEL org.opencontainers.image.source="https://github.com/spin-stack/devcontainer-cli"
LABEL org.opencontainers.image.url="https://github.com/spin-stack/devcontainer-cli"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${REVISION}"

# Distroless already runs as the nonroot user (uid 65532); be explicit.
USER nonroot:nonroot

COPY ${TARGETPLATFORM}/devcontainer /devcontainer

ENTRYPOINT ["/devcontainer"]
