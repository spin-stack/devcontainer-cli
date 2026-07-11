# PRD-E03: OCI Registry Client

**Épica:** E3 — OCI Registry Client
**Depende de:** E1 (Fundaciones)
**Desbloquea:** E4 (Features), E5 (Templates)
**LoC TS equivalente:** ~1.500
**Prioridad:** P0

---

## 1. Contexto

Features y Templates se distribuyen como artefactos OCI en registries (ghcr.io, ACR, ECR, etc.). El CLI implementa un cliente OCI custom para pull/push de manifests y blobs, con autenticación Bearer/Basic y soporte de credential helpers de Docker.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-configuration/containerCollectionsOCI.ts` | 610 | Parsing de refs, manifest pull, blob download, image index |
| `src/spec-configuration/httpOCIRegistry.ts` | 430 | Auth (Bearer/Basic/credHelper), WWW-Authenticate parsing |
| `src/spec-configuration/containerCollectionsOCIPush.ts` | 416 | Push blob/manifest, tag management |

## 2. Objetivo

Un paquete `internal/oci/` que:
1. Parsee OCI references (`registry/namespace/id:tag` o `@sha256:...`).
2. Haga pull de manifests con validación de mediatype `application/vnd.devcontainers`.
3. Descargue blobs (tarballs) con verificación SHA256.
4. Haga push de manifests y blobs para `features publish` y `templates publish`.
5. Maneje autenticación transparente (Bearer token exchange, Basic auth, credential helpers, `GITHUB_TOKEN`, `DEVCONTAINERS_OCI_AUTH`).
6. Cache de auth headers por registry.

## 3. Decisión: `oras-go` vs. implementación propia

**Recomendación:** Usar `oras.land/oras-go/v2` + `github.com/google/go-containerregistry/pkg/authn` como base, con wrappers para la lógica específica de devcontainers.

**Ventajas:**
- Reduce ~1.000 LoC de HTTP/auth/manifest handling propio.
- Manejo maduro de credential helpers y docker config.
- Soporte de multi-platform image index.

**Riesgos:**
- Las mediatypes custom (`application/vnd.devcontainers`) necesitan configuración.
- El fallback `DEVCONTAINERS_OCI_AUTH` es custom y necesita wiring manual.

**Spike previo**: validar que oras-go puede hacer pull de ghcr.io/devcontainers/features/go:latest con auth via GITHUB_TOKEN.

## 4. Diseño

### 4.1 `internal/oci/ref.go` — OCI reference parsing

```go
type OCIRef struct {
    Registry  string // "ghcr.io"
    Owner     string // "devcontainers"
    Namespace string // "devcontainers/features"
    Path      string // "devcontainers/features/go"
    Resource  string // "ghcr.io/devcontainers/features/go"
    ID        string // "go"
    Version   string // tag or digest
    Tag       string // "1.0.0" (optional)
    Digest    string // "sha256:..." (optional)
}

type OCICollectionRef struct {
    Registry string
    Path     string
    Resource string
    Tag      string // always "latest"
}

func ParseRef(input string) (*OCIRef, error)
func ParseCollectionRef(registry, namespace string) (*OCICollectionRef, error)
```

**Reglas de parsing** (paridad exacta con TS):
- Input se lowercasea.
- No puede empezar con `.`.
- `@` separa digest, último `:` (después de último `/`) separa tag.
- Sin tag ni digest → default `latest`.
- Path regex: `^[a-z0-9]+([._-][a-z0-9]+)*(\/[a-z0-9]+([._-][a-z0-9]+)*)*$`
- Version regex: `^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`

### 4.2 `internal/oci/client.go` — Pull/push operations

```go
type Client struct {
    httpClient *httpx.HTTPClient
    log        log.Log
    env        map[string]string
    authCache  map[string]string // registry → auth header
}

func NewClient(log log.Log, env map[string]string) *Client

// Pull
func (c *Client) FetchManifest(ref *OCIRef, expectedDigest string) (*ManifestContainer, error)
func (c *Client) FetchBlob(ref *OCIRef, blobDigest string, destDir string) ([]string, error)
func (c *Client) GetPublishedTags(ref *OCIRef) ([]string, error)
func (c *Client) GetVersionsSorted(ref *OCIRef) ([]string, error)
func (c *Client) GetImageIndexEntry(ref *OCIRef, platform PlatformInfo) (*ImageIndexEntry, error)

// Push
func (c *Client) PushBlob(ref *OCIRef, data []byte) (string, error)
func (c *Client) PushManifest(ref *OCIRef, manifest *OCIManifest, tag string) error
```

### 4.3 `internal/oci/auth.go` — Autenticación

**Cadena de credenciales** (en este orden):
1. Cache de auth header por registry.
2. `DEVCONTAINERS_OCI_AUTH` env var (formato `service|user|token,...`).
3. Docker config (`~/.docker/config.json`):
   - `credHelpers[registry]` → ejecutar `docker-credential-{helper} get`.
   - `credsStore` → ejecutar `docker-credential-{store} get`.
   - `auths[registry].auth` (base64) o `identitytoken` (refresh token).
4. `GITHUB_TOKEN` (solo para `ghcr.io` si `GITHUB_HOST` es `github.com` o unset).
5. Default platform credential helper (`osxkeychain`/`wincred`/`pass`/`secret`).
6. Acceso anónimo.

**Flujo WWW-Authenticate:**
1. Request sin auth → 401.
2. Parse `WWW-Authenticate: Bearer realm="...",service="...",scope="..."`.
3. Si Bearer: POST/GET al realm con credentials → obtener token.
4. Si Basic: enviar credentials directamente.
5. Retry request con `Authorization` header.
6. Cache el header si != 401.

**Refresh token flow** (ACR):
- Si `identitytoken` presente en docker config → POST form-urlencoded con `grant_type=refresh_token`.

### 4.4 Tipos de manifest

```go
type OCIManifest struct {
    SchemaVersion int          `json:"schemaVersion"`
    MediaType     string       `json:"mediaType"`
    Config        OCIDescriptor `json:"config"`
    Layers        []OCILayer   `json:"layers"`
    Annotations   map[string]string `json:"annotations,omitempty"`
    Digest        string       `json:"-"` // computed
}

type ManifestContainer struct {
    Manifest      *OCIManifest
    ManifestBytes []byte
    ContentDigest string
    CanonicalID   string // "resource@digest"
}

const DevcontainerManifestMediaType = "application/vnd.devcontainers"
const DevcontainerTarLayerMediaType = "application/vnd.devcontainers.layer.v1+tar"
const DevcontainerCollectionLayerMediaType = "application/vnd.devcontainers.collection.layer.v1+json"
```

### 4.5 Platform mapping

```go
func NodeArchToGOARCH(arch string) string // "x64" → "amd64"
func NodeOSToGOOS(os string) string       // "win32" → "windows"
```

## 5. Criterios de aceptación

- [ ] `ParseRef` produce resultados idénticos al CLI TS para todos los inputs de `containerFeaturesOCI.test.ts`.
- [ ] Pull manifest de `ghcr.io/devcontainers/features/go:latest` funciona con GITHUB_TOKEN.
- [ ] Push manifest a un registry local (zot) funciona.
- [ ] Credential helper integration funciona en macOS (osxkeychain) y Linux (pass/secret).
- [ ] Digest verification rechaza blobs con hash incorrecto.
- [ ] Auth cache evita requests redundantes al token server.
- [ ] Coverage >= 80%.

## 6. Historias de usuario

### US-E3.1: Parse OCI reference
### US-E3.2: Pull devcontainer feature manifest
### US-E3.3: Download and verify blob
### US-E3.4: Authenticate with Bearer token exchange
### US-E3.5: Authenticate with Docker credential helper
### US-E3.6: Push feature artifact to registry
### US-E3.7: List published tags sorted by semver
### US-E3.8: Resolve multi-platform image index entry

## 7. Tests

- `src/test/container-features/containerFeaturesOCI.test.ts` (310 LoC) — ref parsing, manifest fetch.
- `src/test/container-features/containerFeaturesOCIPush.test.ts` (374 LoC) — push operations.
- `src/test/container-features/registryCompatibilityOCI.test.ts` (172 LoC) — multi-registry compat.
- Integration tests con registry local (usar `ghcr.io/oras-project/zot` como registry de test).

## 8. Riesgos

| Riesgo | Mitigación |
|---|---|
| `oras-go` mediatype filtering puede no cubrir nuestras mediatypes custom | Spike. Si falla, usar HTTP directo. |
| Credential helper subprocess puede fallar silenciosamente | Log detallado en trace mode + fallback a anónimo. |
| ACR refresh token flow es quirky | Test contra ACR real en CI con secrets. |
