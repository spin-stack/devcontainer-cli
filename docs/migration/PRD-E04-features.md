# PRD-E04: Features — Parsing, orden, lockfile y advisories

**Épica:** E4 — Features
**Depende de:** E1 (Fundaciones), E3 (OCI Client)
**Desbloquea:** E9 (Image metadata + extendImage)
**LoC TS equivalente:** ~2.200
**Prioridad:** P1

---

## 1. Contexto

El sistema de Features es el subsistema más complejo del CLI. Resuelve identificadores de features (OCI, tarball, local path, legacy GitHub releases), ordena la instalación por dependencias (`dependsOn`, `installsAfter`), genera lockfiles, y verifica advisories de seguridad.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-configuration/containerFeaturesConfiguration.ts` | 1.261 | Resolución, fetch, schema, install layers |
| `src/spec-configuration/containerFeaturesOrder.ts` | 705 | Grafo de dependencias, topological sort |
| `src/spec-configuration/containerFeaturesOCI.ts` | 74 | OCI feature fetch wrapper |
| `src/spec-configuration/lockfile.ts` | 89 | Lockfile generation/read/write |
| `src/spec-configuration/featureAdvisories.ts` | 93 | Advisory matching por version range |
| `src/spec-configuration/controlManifest.ts` | 110 | Fetch + cache del control manifest |
| `src/spec-node/disallowedFeatures.ts` | 58 | Blocklist check |
| `src/spec-node/featureUtils.ts` | 12 | Thin wrapper |

## 2. Objetivo

Un paquete `internal/features/` que:
1. Parsee feature IDs y los rute al source correcto (OCI/tarball/local/legacy).
2. Construya un grafo de dependencias y compute la orden de instalación.
3. Genere y lea lockfiles.
4. Consulte advisories del control manifest.
5. Verifique features contra la blocklist.

**Nota**: La *instalación* de features (generación de Dockerfile layers) va en E9. Esta épica cubre resolución y ordenamiento.

## 3. Diseño

### 3.1 Feature ID routing

**Tipos de source:**

| Tipo | Ejemplo | Detección |
|---|---|---|
| OCI | `ghcr.io/devcontainers/features/go:1` | Contiene `.` en primer segmento (es un domain) |
| Direct tarball | `https://example.com/feature.tgz` | Empieza con `http://` o `https://` |
| Local file path | `./local-feature` | Empieza con `./` o `../` |
| Legacy shorthand | `go` (deprecated) | No match anterior → auto-map a `ghcr.io/devcontainers/features/{id}` |

```go
type SourceInfo interface {
    Type() string
    UserFeatureID() string
}

type OCISource struct { FeatureRef *oci.OCIRef; ManifestDigest string; ... }
type TarballSource struct { TarballURI string; ... }
type LocalSource struct { LocalPath string; ... }

func ProcessFeatureIdentifier(params Params, workspaceRoot string, feature DevContainerFeature, lockfile *Lockfile) (*FeatureSet, error)
```

### 3.2 Dependency graph and topological sort

**Paridad con:** `containerFeaturesOrder.ts` (705 LoC)

```go
type FNode struct {
    FeatureSet       *FeatureSet
    DependsOn        []*FNode       // hard dependency
    InstallsAfter    []*FNode       // soft dependency
    FeatureIDAliases []string       // legacyIds for matching
}

func BuildDependencyGraph(params Params, features []DevContainerFeature, config *DevContainerConfig, lockfile *Lockfile) (*Graph, error)
func ComputeInstallOrder(params Params, features []DevContainerFeature, config *DevContainerConfig, lockfile *Lockfile, graph *Graph) ([]*FeatureSet, error)
```

**Lógica clave:**
1. **Deduplicación**: Si dos features apuntan al mismo OCI digest + mismas options → colapsar.
2. **Override order**: `overrideFeatureInstallOrder` en config reordena respetando hard deps.
3. **Alias matching**: `legacyIds` permiten detectar que `ghcr.io/devcontainers/features/node` y `ghcr.io/devcontainers/features/javascript` son el mismo feature.
4. **Cycle detection**: set de visitados.

### 3.3 Lockfile

```go
type Lockfile struct {
    Features map[string]LockfileEntry `json:"features"`
}

type LockfileEntry struct {
    Version   string `json:"version"`
    Resolved  string `json:"resolved"`  // registry/path@sha256:...
    Integrity string `json:"integrity"` // sha256:...
}

func GenerateLockfile(featuresConfig *FeaturesConfig) *Lockfile
func ReadLockfile(config *DevContainerConfig) (*Lockfile, bool, error) // lockfile, initLockfile, error
func WriteLockfile(config *DevContainerConfig, lockfile *Lockfile, force bool) error
func GetLockfilePath(configPath string) string
```

**Regla de path**: si config es `.devcontainer.json` → `.devcontainer-lock.json`. Si es `devcontainer.json` → `devcontainer-lock.json`.

### 3.4 Control manifest y advisories

```go
type ControlManifest struct {
    DisallowedFeatures []DisallowedFeature `json:"disallowedFeatures"`
    FeatureAdvisories  []FeatureAdvisory   `json:"featureAdvisories"`
}

func GetControlManifest(cacheFolder string, log Log) (*ControlManifest, error)
func CheckAdvisories(cacheFolder string, log Log, features *FeaturesConfig) []FeatureWithAdvisories
func EnsureNoDisallowedFeatures(params Params, config *DevContainerConfig, additionalFeatures map[string]interface{}, idLabels []string) error
```

**Control manifest:**
- Fetch de `https://containers.dev/static/devcontainer-control-manifest.json`.
- Cache de 5 minutos en `{cacheFolder}/control-manifest.json`.
- Graceful degradation: si fetch falla, usar cache viejo o manifest vacío.

**Advisory matching:**
- `introducedInVersion <= featureVersion < fixedInVersion` → advisory aplica.

### 3.5 Feature auto-mapping (deprecated features)

Mantener la tabla de mapping de shorthand → OCI ref:
```go
var deprecatedFeatureMap = map[string]string{
    "gradle":     "ghcr.io/devcontainers/features/java",
    "maven":      "ghcr.io/devcontainers/features/java",
    "jupyterlab": "ghcr.io/devcontainers/features/python",
    // ... etc
}
```

## 4. Criterios de aceptación

- [ ] Todos los feature IDs de los tests TS se rutean al source correcto.
- [ ] `ComputeInstallOrder` produce el mismo orden que el CLI TS para `containerFeaturesOrder.test.ts` (699 LoC de tests).
- [ ] Lockfile round-trip: generate → write → read produce resultado idéntico.
- [ ] Frozen lockfile mode lanza error si lockfile cambia.
- [ ] Advisories se logean correctamente para features con versiones vulnerables.
- [ ] Disallowed features bloquean el build con el error esperado.
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E4.1: Resolve OCI feature
### US-E4.2: Resolve local feature from path
### US-E4.3: Build dependency graph with dependsOn and installsAfter
### US-E4.4: Deduplicate identical features
### US-E4.5: Apply overrideFeatureInstallOrder
### US-E4.6: Generate and write lockfile
### US-E4.7: Read lockfile and pin versions
### US-E4.8: Detect and log feature advisories
### US-E4.9: Block disallowed features

## 6. Tests a portar

- `src/test/container-features/containerFeaturesOrder.test.ts` (699 LoC) — **crítico**.
- `src/test/container-features/featureHelpers.test.ts` (925 LoC).
- `src/test/container-features/lockfile.test.ts` (251 LoC).
- `src/test/container-features/featureAdvisories.test.ts` (104 LoC).
- `src/test/container-features/generateFeaturesConfig.test.ts` (137 LoC).
- `src/test/disallowedFeatures.test.ts` (60 LoC).

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| Feature dependency graph edge cases (cycles, self-references) | Portar los 699 LoC de tests de order exhaustivamente |
| Legacy GitHub Releases fallback | Documentar deprecation; implementar pero con baja prioridad |
| Feature auto-mapping table puede no estar completa | Extraer la tabla exacta del TS source |
