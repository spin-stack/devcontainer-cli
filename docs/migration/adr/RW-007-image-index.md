# RW-007 — Retire dead OCI image-index types

## Decision

The types `ImageIndex`, `ImageIndexEntry`, and `Platform` (formerly
`internal/oci/types.go:35-55`) and the constant `OCIImageIndexMediaType`
(formerly `internal/oci/ref.go:15`) were **unreferenced** anywhere under
`internal/` (verified by grep across the tree) and have been **deleted**.

In the parity oracle (v0.88.0), OCI image-index resolution is exercised only by
`inspectImageInRegistry` — the registry base-image inspection fallback — which
was **not** ported to Go. Devcontainer Features and Templates are always
single-manifest artifacts, so no path in the Go CLI ever needs to walk a
multi-platform index. Hand-rolled index structs with no caller are dead weight
and a maintenance hazard (they imply support that does not exist).

OCI image-index resolution is only needed if/when `inspectImageInRegistry` is
ported; then it should be implemented via `oras` (which already models indexes,
descriptors, and platform matching), **not** via hand-rolled structs
reintroduced here.
