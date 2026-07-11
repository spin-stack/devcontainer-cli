# PRD-E09: Image metadata + extendImage (Feature installation)

**Épica:** E9 — Image Metadata
**Depende de:** E4 (Features), E6 (Docker engine)
**Desbloquea:** E10 (CLI wiring — cierra el path completo de `up`)
**LoC TS equivalente:** ~1.100
**Prioridad:** P2

---

## 1. Contexto

Esta épica cierra el ciclo de provisioning completo. Cubre dos subsistemas interrelacionados:
1. **Image metadata**: sistema de labels Docker que persiste configuración en imágenes para recovery posterior. Merge semántico de config + metadata de features.
2. **extendImage**: genera un Dockerfile que instala features sobre una imagen base, construye la imagen extendida, y etiqueta con metadata.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-node/imageMetadata.ts` | 491 | Merge de config + image labels, metadata generation |
| `src/spec-node/containerFeatures.ts` | 456 | extendImage, feature layer generation, UID sync |

## 2. Objetivo

Paquetes `internal/imagemeta/` y `internal/provision/extend/` que:
1. Lean metadata de labels de un container/imagen existente.
2. Generen metadata entries desde config + features para persistir en labels.
3. Mergen config con metadata (lifecycle hooks concatenados, env merged, etc.).
4. Generen Dockerfiles con layers de feature installation.
5. Construyan la imagen extendida.
6. Manejen remote user UID/GID sync.

## 3. Diseño

### 3.1 `internal/imagemeta/metadata.go` — Metadata model

```go
type ImageMetadataEntry struct {
    ID                    string                 `json:"id,omitempty"`
    Init                  *bool                  `json:"init,omitempty"`
    Privileged            *bool                  `json:"privileged,omitempty"`
    CapAdd                []string               `json:"capAdd,omitempty"`
    SecurityOpt           []string               `json:"securityOpt,omitempty"`
    Mounts                []MountOrString        `json:"mounts,omitempty"`
    ContainerEnv          map[string]string       `json:"containerEnv,omitempty"`
    ContainerUser         *string                `json:"containerUser,omitempty"`
    RemoteEnv             map[string]*string      `json:"remoteEnv,omitempty"`
    RemoteUser            *string                `json:"remoteUser,omitempty"`
    UpdateRemoteUserUID   *bool                  `json:"updateRemoteUserUID,omitempty"`
    UserEnvProbe          *string                `json:"userEnvProbe,omitempty"`
    OverrideCommand       *bool                  `json:"overrideCommand,omitempty"`
    ForwardPorts          []interface{}          `json:"forwardPorts,omitempty"`
    OnCreateCommand       interface{}            `json:"onCreateCommand,omitempty"`
    UpdateContentCommand  interface{}            `json:"updateContentCommand,omitempty"`
    PostCreateCommand     interface{}            `json:"postCreateCommand,omitempty"`
    PostStartCommand      interface{}            `json:"postStartCommand,omitempty"`
    PostAttachCommand     interface{}            `json:"postAttachCommand,omitempty"`
    WaitFor               *string                `json:"waitFor,omitempty"`
    Customizations        map[string]interface{} `json:"customizations,omitempty"`
    HostRequirements      *HostRequirements      `json:"hostRequirements,omitempty"`
}
```

### 3.2 `internal/imagemeta/labels.go` — Read/write from Docker labels

```go
const MetadataLabel = "devcontainer.metadata"

func GetImageMetadataFromContainer(container *ContainerInspect, config *SubstitutedConfig, featuresConfig *FeaturesConfig, idLabels []string) ([]ImageMetadataEntry, error)
func GetDevcontainerMetadata(baseMetadata []ImageMetadataEntry, config *SubstitutedConfig, featuresConfig *FeaturesConfig) []ImageMetadataEntry
func GenerateMetadataLabel(entries []ImageMetadataEntry) string // JSON compressed
```

**Fuentes de metadata** (en orden):
1. Label `devcontainer.metadata` en la imagen base.
2. Config actual (devcontainer.json).
3. Features instaladas (cada feature aporta su metadata entry).

### 3.3 `internal/imagemeta/merge.go` — Merge semántico

```go
type MergedDevContainerConfig struct {
    // Todos los campos de DevContainerConfig +
    // Lifecycle hooks como arrays (concatenados)
}

func MergeConfiguration(config *DevContainerConfig, imageMetadata []ImageMetadataEntry) *MergedDevContainerConfig
```

**Reglas de merge** (spec compliance):
- **Arrays**: capAdd, securityOpt → union.
- **Lifecycle hooks**: onCreateCommand, etc. → concatenar de todas las entries.
- **Scalars**: remoteUser, containerUser → last-one-wins (última entry = config del usuario).
- **Maps**: containerEnv, remoteEnv → merge con prioridad última entry.
- **Mounts**: dedup por target path, última gana.
- **Ports**: dedup por localhost:port.
- **GPU requirements**: merge complejo (optional/required/object).
- **Customizations**: deep merge.

### 3.4 `internal/provision/extend/extend.go` — Feature installation

```go
func ExtendImage(params *ProvisionParams, config *SubstitutedConfig, baseImage string, imageNames []string, additionalFeatures map[string]interface{}, skipPersist bool) (updatedImageName []string, err error)
```

**Flujo:**
1. Resolve features config (E4).
2. Log advisories.
3. Si no hay features → solo añadir metadata label a imagen.
4. Si hay features:
   a. Crear temp dir con estructura `/tmp/build-features/{n}/`.
   b. Generar `devcontainer-features-install.sh` (wrapper script).
   c. Generar Dockerfile con `FROM base AS final` + `COPY` features + `RUN` install.
   d. Generar metadata label con config comprimido.
   e. `docker build -t {imageName}` con el Dockerfile generado.
5. Handle BuildKit build contexts vs COPY fallback.

### 3.5 Feature layer Dockerfile generation

```go
func GetFeatureLayers(config *FeaturesConfig, containerUser, remoteUser string) (string, error)
```

Genera líneas tipo:
```dockerfile
COPY --from=dev_containers_feature_content_source {featureN}/ /tmp/build-features/{featureN}/
RUN chmod +x /tmp/build-features/{featureN}/install.sh \
    && /tmp/build-features/{featureN}/install.sh
```

Con environment variables por feature (`_BUILD_ARG_*`).

### 3.6 UID/GID sync

```go
func UpdateRemoteUserUID(params *Params, config *MergedConfig, imageName string, remoteUser string) error
```

Genera un Dockerfile snippet basado en `scripts/updateUID.Dockerfile` que remapea el user.

## 4. Criterios de aceptación

- [ ] Metadata read/write roundtrip: write label → inspect → read produce entries idénticas.
- [ ] Merge de config + metadata produce resultado idéntico al CLI TS para `imageMetadata.test.ts` (577 LoC).
- [ ] Feature Dockerfile generation produce layers funcionales.
- [ ] Extended image contiene la metadata label correcta.
- [ ] Images built by TS CLI son leíbles por Go CLI (y viceversa) — **backward compat crítico**.
- [ ] UID sync funciona para amd64 y arm64.
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E9.1: Read image metadata from Docker labels
### US-E9.2: Generate metadata entries from config + features
### US-E9.3: Merge config with image metadata (lifecycle hook concatenation)
### US-E9.4: Generate feature installation Dockerfile
### US-E9.5: Build extended image with features
### US-E9.6: Persist metadata label on built image
### US-E9.7: Cross-CLI backward compatibility (TS labels readable by Go)
### US-E9.8: Update remote user UID/GID

## 6. Tests a portar

- `src/test/imageMetadata.test.ts` (577 LoC) — **crítico**.
- `src/test/container-features/e2e.test.ts` (255 LoC) — feature installation E2E.
- `src/test/updateUID.test.ts` (110 LoC).
- Golden tests: `build` con features produce imágenes con metadata correcta.

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| Metadata label format debe ser idéntico entre TS y Go | Golden test con imágenes construidas por CLI TS |
| Feature install script generation es frágil | Comparar Dockerfiles generados vs CLI TS output |
| BuildKit context mounting vs COPY fallback | Tests con y sin BuildKit |
