package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

func renderDependencyMermaid(client oci.Registry, logger log.Logger, roots []string) string {
	userFeatures := make([]features.DevContainerFeature, 0, len(roots))
	for _, r := range roots {
		userFeatures = append(userFeatures, features.DevContainerFeature{
			UserFeatureID: r,
			Options:       map[string]any{},
		})
	}
	graph, err := features.BuildDependencyGraph(logger, newMetadataProcessFeature(client, logger, localFeatureRoots{}, nil), userFeatures, nil, nil)
	if err != nil {
		logger.Write(fmt.Sprintf("Could not build dependency graph: %v", err), log.LevelTrace)
		return "flowchart\n"
	}
	return features.GenerateMermaidDiagram(graph)
}

func newMetadataProcessFeature(client oci.Registry, logger log.Logger, local localFeatureRoots, lockfile *features.Lockfile) features.ProcessFeature {
	return func(node *features.FNode) (*features.Set, error) {
		id := node.UserFeatureID
		resolvedID, srcType, err := resolveFeatureID(logger, id)
		if err != nil {
			return nil, err
		}

		switch srcType {
		case features.SourceLocalPath:
			resolvedPath, ok, err := local.resolve(logger, id)
			if !ok {
				return nil, err
			}
			meta, err := readLocalFeatureMetadata(resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
			}
			meta.Value, meta.ValueOrder = node.Options, node.OptionOrder
			if meta.ID == "" {
				meta.ID = id
			}
			return &features.Set{
				SourceInfo: &features.LocalSource{LocalPath: id, ResolvedPath: resolvedPath, UserID: id},
				Features:   []features.Feature{meta},
			}, nil

		case features.SourceDirectTarball:
			meta, err := readTarballFeatureMetadata(logger, id, lockfile)
			if _, ok := errors.AsType[*digestMismatchError](err); ok {
				return nil, err
			}
			if err != nil {
				return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
			}
			meta.Value, meta.ValueOrder = node.Options, node.OptionOrder
			return &features.Set{
				SourceInfo: &features.TarballSource{TarballURI: id, UserID: id},
				Features:   []features.Feature{meta},
			}, nil

		default:
			ref, err := oci.ParseRef(resolvedID)
			if err != nil {
				return nil, unparseableFeatureRef(logger, id, resolvedID, err)
			}
			if lockfile != nil {
				if integrity := lockfile.Features[resolvedID].Integrity; integrity != "" {
					if pinned, perr := oci.ParseRef(ref.Resource + "@" + integrity); perr == nil {
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
			meta.Value, meta.ValueOrder = node.Options, node.OptionOrder

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

func readOCIFeatureMetadata(client oci.Registry, ref *oci.Ref, manifest *oci.ManifestContainer) features.Feature {
	if mj, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok && mj != "" {
		var meta features.Feature
		json.Unmarshal([]byte(mj), &meta)
		return meta
	}
	if len(manifest.Manifest.Layers) == 0 {
		return features.Feature{}
	}
	blobData, err := client.FetchBlob(ref, manifest.Manifest.Layers[0].Digest)
	if err != nil {
		return features.Feature{}
	}
	meta, _ := featureMetadataFromTarball(blobData)
	return meta
}

func featureMetadataFromTarball(blob []byte) (features.Feature, error) {
	tmp, err := os.MkdirTemp("", "devcontainer-feature-meta-")
	if err != nil {
		return features.Feature{}, err
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "feature.tgz")
	if err := os.WriteFile(tgz, blob, 0o644); err != nil {
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

func ociAnnotationID(manifest *oci.ManifestContainer) string {
	var meta features.Feature
	if json.Unmarshal([]byte(manifest.Manifest.Annotations["dev.containers.metadata"]), &meta) != nil {
		return ""
	}
	return meta.ID
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

func readTarballFeatureMetadata(logger log.Logger, tarballURL string, lockfile *features.Lockfile) (features.Feature, error) {
	blobData, digest, err := downloadFeatureTarball(context.Background(), logger, tarballURL, osEnvMap())
	if err != nil {
		return features.Feature{}, err
	}
	if err := verifyTarballDigest(lockfile, tarballURL, digest); err != nil {
		return features.Feature{}, err
	}
	return featureMetadataFromTarball(blobData)
}
