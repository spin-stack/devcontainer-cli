# RW-008 — Registry authentication

## Scope

Linux-only, Docker-only. Credential helpers in scope: `secretservice` (libsecret)
and `pass`. macOS (`osxkeychain`) and Windows (`wincred`) helper names are kept in
the switch for parity with the TS surface but are **not** supported targets and
are **not** tested.

## Divergences from the TS oracle (both handled)

1. **Default Linux helper name.** The TS CLI names the libsecret helper `secret`.
   No `docker-credential-secret` binary exists; the real one is
   `docker-credential-secretservice`. Go keeps the correct `secretservice`
   (`internal/oci/auth.go`, `linuxDefaultHelperName`). Pinned by
   `TestDefaultCredentialHelperName`.

2. **Auth cache lifetime.** TS keeps the auth cache per CLI invocation; Go
   previously built a fresh `auth.NewCache()` per `repository()` call
   (per-operation). RW-008 hoists a single `auth.Cache` onto `oci.Client`
   (`client.go`), so an auth challenge negotiated for one operation (e.g. a push)
   is reused by related operations (e.g. the following tag list / manifest fetch)
   instead of re-running the 401→WWW-Authenticate→token loop each time. A
   zero-value `Client` still falls back to a per-repository cache, so the change
   is behavior-preserving for callers that bypass `NewClient`. Asserted by
   `TestClientAuthCacheReused`.

## Hermetic tests (live now)

- `TestRegistryBasicAuthLoop` (`internal/oci/registry_auth_test.go`): a real
  `registry:3` behind htpasswd Basic auth (testcontainers) drives the full oras
  loop — first request 401 + `WWW-Authenticate`, credential read from a temporary
  `DOCKER_CONFIG` auths entry, authenticated retry, push + tag-list + pull. A
  negative control asserts an anonymous client is rejected (auth is enforced).
- `TestGetCredentialFromHelper_Protocol` and `TestCredHelperViaDockerConfig`
  (`internal/oci/auth_helper_test.go`): a fake `docker-credential-faketest`
  executable on `PATH` drives the credential-helper `get` protocol end-to-end,
  covering both the ordinary Basic path and the previously-untested
  `<token>`→`refreshToken` branch, directly and via a docker config `credHelpers`
  entry.

### Skip conditions

- Tests needing the Docker daemon `t.Skip` when `docker info` fails.
- The fake-helper tests `t.Skip` on non-POSIX platforms (shell script helper).

## Cloud matrix (deferred, gated by secrets)

Real cloud registries exercise the bearer/token-server flow that a plain htpasswd
`registry:3` does not (it uses `WWW-Authenticate: Basic`). These are **deferred**
and must be gated on repository secrets so the suite stays hermetic by default:

| Registry | Flow exercised                         | Gate (secret)                     |
|----------|----------------------------------------|-----------------------------------|
| GHCR     | `GITHUB_TOKEN` → Basic `USERNAME:tok`  | `GITHUB_TOKEN`                    |
| ACR      | identitytoken → `<token>` refreshToken | ACR service-principal creds       |
| ECR      | `docker-credential-ecr-login` helper   | AWS creds + region                |

Each cloud case must `t.Skip` when its secret is absent. Wiring lands with the
RW-018 network lane; not blocking for the hermetic milestone.
