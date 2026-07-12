package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// renderDependencyMermaid resolves the dependency graph rooted at the given
// feature ids through the unified features.BuildDependencyGraph builder and
// renders it as a mermaid flowchart (TS generateMermaidDiagram). It is used by
// `features info` (single OCI root). Node hashes are internally consistent but
// need not match the TS hashes byte-for-byte — the parity harness scrubs them.
func renderDependencyMermaid(client oci.Registry, logger log.Logger, roots []string) string {
	userFeatures := make([]features.DevContainerFeature, 0, len(roots))
	for _, r := range roots {
		userFeatures = append(userFeatures, features.DevContainerFeature{
			UserFeatureID: r,
			Options:       map[string]interface{}{},
		})
	}
	graph, err := features.BuildDependencyGraph(logger, newMetadataProcessFeature(client, logger, "", nil), userFeatures, nil, nil)
	if err != nil {
		logger.Write(fmt.Sprintf("Could not build dependency graph: %v", err), log.LevelTrace)
		return "flowchart\n"
	}
	return features.GenerateMermaidDiagram(graph)
}

// newMetadataProcessFeature returns a read-only processFeature seam that resolves
// a feature only far enough to read its dependency metadata (id, legacyIds,
// dependsOn, installsAfter). It does not stage install content, so it is cheap
// enough for the resolve-dependencies and mermaid consumers. OCI metadata is read
// annotation-first with a blob fallback, matching the TS getOCIFeatureMetadata.
func newMetadataProcessFeature(client oci.Registry, logger log.Logger, basePath string, lockfile *features.Lockfile) features.ProcessFeature {
	return func(node *features.FNode) (*features.Set, error) {
		id := node.UserFeatureID
		srcType := features.ClassifyID(id)

		switch srcType {
		case features.SourceLocalPath:
			resolvedPath := id
			if !filepath.IsAbs(resolvedPath) {
				resolvedPath = filepath.Join(basePath, resolvedPath)
			}
			resolvedPath = filepath.Clean(resolvedPath)
			meta, err := readLocalFeatureMetadata(resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
			}
			meta.Value = node.Options
			if meta.ID == "" {
				meta.ID = id
			}
			return &features.Set{
				SourceInfo: &features.LocalSource{LocalPath: id, ResolvedPath: resolvedPath, UserID: id},
				Features:   []features.Feature{meta},
			}, nil

		case features.SourceDirectTarball:
			meta, err := readTarballFeatureMetadata(logger, id)
			if err != nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
			}
			meta.Value = node.Options
			return &features.Set{
				SourceInfo: &features.TarballSource{TarballURI: id, UserID: id},
				Features:   []features.Feature{meta},
			}, nil

		default: // OCI (and legacy shorthand resolved to OCI)
			resolvedID, _ := features.ResolveID(id, false)
			ref, err := oci.ParseRef(resolvedID)
			if err != nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
			}
			if lockfile != nil {
				if entry, ok := lockfile.Features[id]; ok && entry.Integrity != "" {
					if pinned, perr := oci.ParseRef(ref.Resource + "@" + entry.Integrity); perr == nil {
						ref = pinned
					}
				}
			}
			logger.Write(fmt.Sprintf("Resolving Feature dependencies for '%s'...", id), log.LevelInfo)

			manifest, err := client.FetchManifest(ref, "")
			if err != nil || manifest.Manifest == nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  You may not have permission to access this Feature, or may not be logged in.", id)
			}

			meta := readOCIFeatureMetadata(client, ref, manifest)
			meta.ID = ref.ID
			if annoID := ociAnnotationID(manifest); annoID != "" {
				meta.ID = annoID
			}
			meta.Value = node.Options

			return &features.Set{
				SourceInfo: &features.OCISource{
					Type:     "oci",
					Registry: ref.Registry, Namespace: ref.Namespace,
					ID: ref.ID, Resource: ref.Resource, Tag: ref.Tag,
					ManifestDigest: manifest.ContentDigest, UserID: id,
					Manifest: manifest.Manifest,
				},
				Features:       []features.Feature{meta},
				ComputedDigest: manifest.ContentDigest,
			}, nil
		}
	}
}

// readOCIFeatureMetadata reads a feature's metadata annotation-first, falling
// back to the extracted devcontainer-feature.json blob when the manifest omits
// the dev.containers.metadata annotation (TS getOCIFeatureMetadata).
func readOCIFeatureMetadata(client oci.Registry, ref *oci.Ref, manifest *oci.ManifestContainer) features.Feature {
	if mj, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok && mj != "" {
		var meta features.Feature
		json.Unmarshal([]byte(mj), &meta)
		return meta
	}
	// Blob fallback: fetch and extract the layer to read devcontainer-feature.json.
	if len(manifest.Manifest.Layers) == 0 {
		return features.Feature{}
	}
	blobData, err := client.FetchBlob(ref, manifest.Manifest.Layers[0].Digest)
	if err != nil {
		return features.Feature{}
	}
	tmp, err := os.MkdirTemp("", "devcontainer-feature-meta-")
	if err != nil {
		return features.Feature{}
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "feature.tgz")
	if err := os.WriteFile(tgz, blobData, 0644); err != nil {
		return features.Feature{}
	}
	if err := extractTarGz(tgz, tmp); err != nil {
		return features.Feature{}
	}
	var meta features.Feature
	if data, err := os.ReadFile(filepath.Join(tmp, "devcontainer-feature.json")); err == nil {
		json.Unmarshal(data, &meta)
	}
	return meta
}

func ociAnnotationID(manifest *oci.ManifestContainer) string {
	if mj, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok && mj != "" {
		var meta features.Feature
		if json.Unmarshal([]byte(mj), &meta) == nil {
			return meta.ID
		}
	}
	return ""
}

func readLocalFeatureMetadata(dir string) (features.Feature, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return features.Feature{}, fmt.Errorf("Local Feature path '%s' not found.", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "devcontainer-feature.json"))
	if err != nil {
		return features.Feature{}, err
	}
	var meta features.Feature
	if err := json.Unmarshal(data, &meta); err != nil {
		return features.Feature{}, err
	}
	return meta, nil
}

func readTarballFeatureMetadata(logger log.Logger, tarballURL string) (features.Feature, error) {
	blobData, _, err := downloadFeatureTarball(context.Background(), logger, tarballURL, osEnvMap())
	if err != nil {
		return features.Feature{}, err
	}
	tmp, err := os.MkdirTemp("", "devcontainer-feature-meta-")
	if err != nil {
		return features.Feature{}, err
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "feature.tgz")
	if err := os.WriteFile(tgz, blobData, 0644); err != nil {
		return features.Feature{}, err
	}
	if err := extractTarGz(tgz, tmp); err != nil {
		return features.Feature{}, err
	}
	var meta features.Feature
	if data, err := os.ReadFile(filepath.Join(tmp, "devcontainer-feature.json")); err == nil {
		json.Unmarshal(data, &meta)
	}
	return meta, nil
}
