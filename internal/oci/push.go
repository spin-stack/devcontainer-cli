package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/devcontainers/cli/internal/log"
)

// GetSemanticTags computes the tags to publish for `version` given the tags
// already present in the registry, matching the TS CLI getSemanticTags:
//   - skip=true (nil tags) when the exact version already exists — do not
//     republish;
//   - err when `version` is not a valid semantic version;
//   - the floating tags (major, major.minor, latest) are only included when the
//     new version is the highest within their range, so a re-publish of an older
//     version never moves latest/major/major.minor backwards.
func GetSemanticTags(version string, publishedTags []string) (tags []string, skip bool, err error) {
	for _, t := range publishedTags {
		if t == version {
			return nil, true, nil
		}
	}
	v, err := semver.StrictNewVersion(version)
	if err != nil {
		return nil, false, fmt.Errorf("version %q is not a valid semantic version", version)
	}

	// Only full X.Y.Z tags participate in the max-in-range comparison.
	var pub []*semver.Version
	for _, t := range publishedTags {
		if pv, e := semver.StrictNewVersion(t); e == nil {
			pub = append(pub, pv)
		}
	}
	advances := func(inRange func(*semver.Version) bool) bool {
		var max *semver.Version
		for _, pv := range pub {
			if inRange(pv) && (max == nil || pv.GreaterThan(max)) {
				max = pv
			}
		}
		return max == nil || v.GreaterThan(max)
	}

	if advances(func(pv *semver.Version) bool { return pv.Major() == v.Major() }) {
		tags = append(tags, fmt.Sprintf("%d", v.Major()))
	}
	if advances(func(pv *semver.Version) bool { return pv.Major() == v.Major() && pv.Minor() == v.Minor() }) {
		tags = append(tags, fmt.Sprintf("%d.%d", v.Major(), v.Minor()))
	}
	tags = append(tags, version)
	if advances(func(pv *semver.Version) bool { return true }) {
		tags = append(tags, "latest")
	}
	return tags, false, nil
}

// PushResult holds the result of pushing an artifact to a registry.
type PushResult struct {
	Digest        string   `json:"digest"`
	PublishedTags []string `json:"publishedTags"`
}

// PushArtifact uploads a tarball as an OCI artifact to a registry via oras-go.
// It creates a manifest with the devcontainer mediatype and pushes the layer +
// empty config blobs, then the manifest under each computed version tag. oras-go
// handles auth (pull,push scope negotiation), blob-existence checks and retries.
func (c *Client) PushArtifact(ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error) {
	c.log.Write(fmt.Sprintf("Pushing %s to %s (tags: %s)...", ref.ID, ref.Resource, strings.Join(tags, ", ")), log.LevelInfo)
	if collectionType == "" {
		collectionType = "feature"
	}

	ctx := context.Background()
	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}

	// Read tarball
	tgzData, err := os.ReadFile(tgzPath)
	if err != nil {
		return nil, fmt.Errorf("read tarball: %w", err)
	}

	// Upload the layer blob.
	layerDigest := godigest.FromBytes(tgzData)
	layerDesc := ocispec.Descriptor{
		MediaType: TarLayerMediaType,
		Digest:    layerDigest,
		Size:      int64(len(tgzData)),
		Annotations: map[string]string{
			"org.opencontainers.image.title": fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, ref.ID),
		},
	}
	if err := pushBlobIfAbsent(ctx, repo, layerDesc, tgzData); err != nil {
		return nil, fmt.Errorf("upload blob: %w", err)
	}

	// Upload the empty config blob (required by the OCI spec).
	configData := []byte("{}")
	configDigest := godigest.FromBytes(configData)
	configDesc := ocispec.Descriptor{
		MediaType: ManifestMediaType,
		Digest:    configDigest,
		Size:      int64(len(configData)),
	}
	if err := pushBlobIfAbsent(ctx, repo, configDesc, configData); err != nil {
		return nil, fmt.Errorf("upload config: %w", err)
	}

	// Build the manifest with the local type to keep the exact byte layout.
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     OCIManifestMediaType,
		Config: Descriptor{
			Digest:    configDigest.String(),
			MediaType: ManifestMediaType,
			Size:      int64(len(configData)),
		},
		Layers: []Layer{
			{
				MediaType:   TarLayerMediaType,
				Digest:      layerDigest.String(),
				Size:        int64(len(tgzData)),
				Annotations: layerDesc.Annotations,
			},
		},
		Annotations: annotations,
	}

	manifestBytes, _ := json.Marshal(manifest)
	manifestDesc := ocispec.Descriptor{
		MediaType: OCIManifestMediaType,
		Digest:    godigest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// Push the manifest under each version tag (PushReference does PUT
	// /manifests/<tag>, tagging in the same request).
	var publishedTags []string
	for _, tag := range tags {
		if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), tag); err != nil {
			c.log.Write(fmt.Sprintf("Failed to push tag %s: %v", tag, err), log.LevelWarning)
			continue
		}
		publishedTags = append(publishedTags, tag)
		c.log.Write(fmt.Sprintf("Published tag: %s", tag), log.LevelInfo)
	}

	if len(publishedTags) == 0 {
		return nil, fmt.Errorf("failed to publish any tags")
	}

	return &PushResult{
		Digest:        manifestDesc.Digest.String(),
		PublishedTags: publishedTags,
	}, nil
}

// PushCollectionMetadata publishes the devcontainer-collection.json for a
// namespace as an OCI artifact tagged `latest`, so containers.dev and the CLI
// can discover the collection's items — matching the TS CLI doPublishMetadata.
// The ref should point at the collection (registry/namespace), not an item.
func (c *Client) PushCollectionMetadata(ref *Ref, collectionJSONPath string) (*PushResult, error) {
	c.log.Write(fmt.Sprintf("Publishing collection metadata to %s...", ref.Resource), log.LevelInfo)

	ctx := context.Background()
	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(collectionJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read collection metadata: %w", err)
	}

	layerDigest := godigest.FromBytes(data)
	layerDesc := ocispec.Descriptor{
		MediaType: CollectionLayerMediaType,
		Digest:    layerDigest,
		Size:      int64(len(data)),
		Annotations: map[string]string{
			"org.opencontainers.image.title": "devcontainer-collection.json",
		},
	}
	if err := pushBlobIfAbsent(ctx, repo, layerDesc, data); err != nil {
		return nil, fmt.Errorf("upload collection layer: %w", err)
	}

	configData := []byte("{}")
	configDigest := godigest.FromBytes(configData)
	configDesc := ocispec.Descriptor{
		MediaType: ManifestMediaType,
		Digest:    configDigest,
		Size:      int64(len(configData)),
	}
	if err := pushBlobIfAbsent(ctx, repo, configDesc, configData); err != nil {
		return nil, fmt.Errorf("upload config: %w", err)
	}

	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     OCIManifestMediaType,
		Config: Descriptor{
			Digest:    configDigest.String(),
			MediaType: ManifestMediaType,
			Size:      int64(len(configData)),
		},
		Layers: []Layer{
			{
				MediaType:   CollectionLayerMediaType,
				Digest:      layerDigest.String(),
				Size:        int64(len(data)),
				Annotations: layerDesc.Annotations,
			},
		},
		Annotations: map[string]string{
			"com.github.package.type": "devcontainer_collection",
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestDesc := ocispec.Descriptor{
		MediaType: OCIManifestMediaType,
		Digest:    godigest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), "latest"); err != nil {
		return nil, fmt.Errorf("push collection manifest: %w", err)
	}

	return &PushResult{Digest: manifestDesc.Digest.String(), PublishedTags: []string{"latest"}}, nil
}

// pushBlobIfAbsent pushes a blob unless the registry already has it.
func pushBlobIfAbsent(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, data []byte) error {
	if exists, err := repo.Blobs().Exists(ctx, desc); err == nil && exists {
		return nil
	}
	return repo.Blobs().Push(ctx, desc, bytes.NewReader(data))
}

// computeTags derives the version tags to publish (X.Y.Z, X.Y, X, latest).
func computeTags(version string) []string {
	parts := strings.Split(version, ".")
	tags := []string{version}
	if len(parts) >= 3 {
		tags = append(tags, strings.Join(parts[:2], "."))
	}
	if len(parts) >= 2 {
		tags = append(tags, parts[0])
	}
	tags = append(tags, "latest")
	return tags
}
