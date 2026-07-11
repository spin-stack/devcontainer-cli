# PRD-E05: Templates

**Épica:** E5 — Templates
**Depende de:** E3 (OCI Client)
**Desbloquea:** E10 (CLI wiring — templates subcommands)
**LoC TS equivalente:** ~500
**Prioridad:** P1

---

## 1. Contexto

Templates son artefactos OCI que contienen archivos de proyecto (devcontainer.json, Dockerfile, etc.) que se aplican a un workspace. El sistema de templates es análogo al de features pero más simple: no hay dependencias, orden ni lockfile.

**Archivos TS fuente:**

| Archivo | LoC | Propósito |
|---|---|---|
| `src/spec-configuration/containerTemplatesConfiguration.ts` | 33 | Schema mínimo |
| `src/spec-configuration/containerTemplatesOCI.ts` | 176 | Fetch template de OCI, apply a workspace |
| `src/spec-node/templatesCLI/apply.ts` | 153 | Comando `templates apply` |
| `src/spec-node/templatesCLI/publish.ts` | 132 | Comando `templates publish` |
| `src/spec-node/templatesCLI/metadata.ts` | 75 | Comando `templates metadata` |
| `src/spec-node/templatesCLI/generateDocs.ts` | 54 | Comando `templates generate-docs` |
| `src/spec-node/templatesCLI/packageImpl.ts` | 57 | Package templates como tarballs |
| `src/spec-node/collectionCommonUtils/packageCommandImpl.ts` | 267 | Shared packaging logic |
| `src/spec-node/collectionCommonUtils/publishCommandImpl.ts` | 83 | Shared publish logic |
| `src/spec-node/collectionCommonUtils/generateDocsCommandImpl.ts` | 198 | Shared docs generation |

## 2. Objetivo

Un paquete `internal/templates/` + handlers de comando que implementen:
1. `templates apply` — Fetch template de OCI, extraer, aplicar args, copiar a workspace.
2. `templates publish` — Package + push a registry.
3. `templates metadata` — Fetch y mostrar metadata de un template publicado.
4. `templates generate-docs` — Generar markdown docs desde template metadata.
5. Shared logic con features: packaging (tarball creation), publishing (OCI push).

## 3. Diseño

### 3.1 Template apply flow

```go
func FetchAndApplyTemplate(params Params, selected SelectedTemplate, workspaceFolder string, tmpDir string) ([]string, error)
```

1. Parse template ID como `OCIRef`.
2. Fetch manifest de OCI registry.
3. Download blob (tarball).
4. Extract a temp dir, respetando `omitPaths`.
5. Apply template args: reemplazar `${templateOption:name}` en archivos.
6. Add features to generated devcontainer.json si `--features` provided.
7. Copy a workspace folder.
8. Return lista de archivos creados.

### 3.2 Shared packaging logic

```go
// Reutilizado por features y templates
func PackageCollection(host CLIHost, targetFolder string, outputDir string, collectionType string) (*CollectionMetadata, error)
func PublishToRegistry(params Params, version string, ref *OCIRef, outputDir string, collectionType string, archiveName string, annotations map[string]string) (*PublishResult, error)
func PublishCollectionMetadata(params Params, ref *OCICollectionRef, outputDir string, collectionType string) error
func GenerateDocumentation(collectionFolder string, registry string, namespace string, owner string, repo string, log Log) error
```

## 4. Criterios de aceptación

- [ ] `templates apply` produce los mismos archivos que el CLI TS para los fixtures de test.
- [ ] Template args se reemplazan correctamente en archivos generados.
- [ ] `omit-paths` filtra archivos durante extracción.
- [ ] `templates publish` pushea correctamente a un registry local.
- [ ] `templates metadata` muestra metadata del manifest annotation.
- [ ] Coverage >= 80%.

## 5. Historias de usuario

### US-E5.1: Apply template from OCI to workspace
### US-E5.2: Replace template args in generated files
### US-E5.3: Package templates as tarballs
### US-E5.4: Publish templates to OCI registry
### US-E5.5: Fetch and display template metadata
### US-E5.6: Generate documentation from template metadata

## 6. Tests a portar

- `src/test/container-templates/templatesCLICommands.test.ts` (274 LoC).
- `src/test/container-templates/containerTemplatesOCI.test.ts` (236 LoC).

## 7. Riesgos

| Riesgo | Mitigación |
|---|---|
| Template arg substitution syntax puede tener edge cases | Portar tests de templatesCLICommands exhaustivamente |
| Shared packaging logic tiene coupling con features | Refactorizar en Go como paquete compartido limpio |
