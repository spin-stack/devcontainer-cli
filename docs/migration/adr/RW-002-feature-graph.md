# RW-002 — Unificar el grafo de dependencias de Features

Estado: aceptado · Track: T1 (Features graph) · Oráculo: `reference/` v0.88.0
(`src/spec-configuration/containerFeaturesOrder.ts`)

## Contexto

Go tenía **tres** constructores de grafo independientes y divergentes:

- **install** (`feature_install.go`): fetch recursivo + `dependencyNodes` /
  `populateHardDepEdges` / `populateSoftDepEdges` + `ComputeInstallationOrder`.
- **resolve-dependencies JSON** (`features_resolve_deps.go`): construía nodos
  **sin ninguna arista** de dependencia — su `installOrder` ignoraba las
  dependencias transitivas (bug en producción).
- **mermaid** (`feature_deps.go`): un BFS propio con `roundPriority` fijo en 0.

TS tiene **uno solo**: `buildDependencyGraph` + `computeDependsOnInstallationOrder`
+ `generateMermaidDiagram`, todos parametrizados por un closure `processFeature`.

## Decisión

Se introduce un único constructor en `internal/features/graph.go`:

```go
type ProcessFeature func(node *FNode) (*FeatureSet, error)

func BuildDependencyGraph(
    logger log.Log,
    processFeature ProcessFeature,
    userFeatures []DevContainerFeature,
    overrideOrder []string,
    lockfile *Lockfile,
) ([]*FNode, error)
```

y `GenerateMermaidDiagram([]*FNode) string`. Los tres consumidores enrutan por
aquí.

### El seam `processFeature`

`processFeature` es el punto de inyección (equivalente al closure homónimo de
TS). Dado el `UserFeatureID` + `Options` de un nodo, devuelve un `FeatureSet` con
`SourceInfo` poblado y `Features[0]` con la metadata de dependencias
(`id`/`legacyIds`/`dependsOn`/`installsAfter`), leída **annotation-first con
fallback a blob** para OCI. Devolver `(nil, nil)` significa "no se pudo procesar"
(equivale al `undefined` de TS) y el builder lo convierte en error.

Hay dos implementaciones del seam:

- **install** (`processInstallFeature` en `feature_install.go`): hace el fetch y
  **extrae** el contenido a un directorio de staging (necesario para construir la
  imagen). El fetch del install-path **migró** aquí: antes era el cuerpo del loop
  de `fetchFeatureSetsRecursive`; ahora es la implementación del seam. La lectura
  de metadata es annotation-first: primero se lee `devcontainer-feature.json` del
  blob extraído (fallback), y si el manifiesto trae la anotación
  `dev.containers.metadata` ésta **sobrescribe** los campos de dependencia.
- **read-only** (`newMetadataProcessFeature` en `feature_deps.go`): resuelve sólo
  lo necesario para leer metadata (sin stagear contenido de instalación). Lo usan
  `features info` (mermaid) y `resolve-dependencies`. Para OCI lee annotation
  primero y sólo baja el blob si falta la anotación (equivale a
  `getOCIFeatureMetadata` de TS).

### Grafo precomputado

`ComputeInstallationOrder(logger, nodes, overrideOrder)` ya aceptaba `[]*FNode`:
ese slice **es** el "grafo precomputado". Los consumidores llaman
`BuildDependencyGraph(...)` una vez y pasan el worklist resultante a
`ComputeInstallationOrder(..., nil)`. El `overrideFeatureInstallOrder` se aplica
**dentro** de `BuildDependencyGraph` (como en TS, porque el matching del override
requiere `processFeature` para resolver identidad OCI + `legacyIds`), por eso se
pasa `nil` como override a `ComputeInstallationOrder` para no aplicarlo dos veces.
El parámetro `overrideOrder` de `ComputeInstallationOrder` se conserva sólo para
`order_test.go` (matcher por string), que no toca el path de producción.

### Quirks de TS preservados

- **Soft-deps (`installsAfter`) no se expanden recursivamente** (no se agregan al
  worklist) **pero sí se les hace `processFeature`** para leer sus `legacyIds` y
  poder hacer el matching por alias.
- **Identidad de dedup = recurso + opciones**, no el string de entrada. Se portó
  `optionsCompareTo` (TS) y se integró en `compareNodes` (OCI/local/tarball),
  incluido el short-circuit de OCI (`digest igual && opciones iguales`).
- **Los ciclos abortan**: `_buildDependencyGraph` no reporta ciclos (sólo evita
  el loop con el dedup del acumulador); el cálculo por rondas de
  `ComputeInstallationOrder` los detecta y devuelve error.
- **Override**: prioridad `N..1` de la primera a la última entrada; matching por
  `satisfiesSoftDependency` (identidad OCI + `legacyIds`); una entrada inválida
  **aborta** (antes Go la ignoraba en silencio) — esto habilita RW-001.

### Realineación de directorios (install)

`processInstallFeature` extrae en `_dev_container_feature_stage_<n>`. Tras
calcular el orden, `realignFeatureDirs` renombra cada staging al índice final
`_dev_container_feature_<i>` que espera el `COPY` del Dockerfile generado, y borra
los staging huérfanos (duplicados y probes de soft-dep no instalados). Esto además
cierra una desalineación latente: antes el orden final podía no coincidir con el
índice físico del directorio.

## Consecuencias

- El bug de `resolve-dependencies` (sin aristas) queda resuelto: su
  `installOrder` ahora respeta dependencias transitivas y el override.
- Mermaid emite `roundPriority` reales y aristas hard (`-->`) / soft (`-.->`)
  derivadas del mismo grafo.
- RW-001 colapsa a cablear `cfg.OverrideFeatureInstallOrder` por
  `FeatureBuildOptions.OverrideFeatureInstallOrder` hasta el builder.
- Costo: en el install-path los soft-deps se descargan/extraen para leer
  `legacyIds` aunque no se instalen (fidelidad con TS; optimizable con caché).
- Los hashes de los nodos mermaid son internamente consistentes pero **no**
  byte-idénticos a TS; el harness de paridad los depura (scrub).
