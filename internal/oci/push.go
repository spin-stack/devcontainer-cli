package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/devcontainers/cli/internal/log"
)

func SemanticTags(version string, publishedTags []string) (tags []string, skip bool, err error) {
	if slices.Contains(publishedTags, version) {
		return nil, true, nil
	}
	v, err := semver.StrictNewVersion(version)
	if err != nil {
		return nil, false, fmt.Errorf("version %q is not a valid semantic version", version)
	}

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

type PushResult struct {
	Digest        string   `json:"digest"`
	PublishedTags []string `json:"publishedTags"`
	Version       string   `json:"version,omitempty"`
}

func (c *Client) PushArtifact(ctx context.Context, ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error) {
	c.log.Write(fmt.Sprintf("Pushing %s to %s (tags: %s)...", ref.ID, ref.Resource, strings.Join(tags, ", ")), log.LevelInfo)
	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}

	tgzData, err := os.ReadFile(tgzPath)
	if err != nil {
		return nil, fmt.Errorf("read tarball: %w", err)
	}

	manifestDesc, manifestBytes, err := pushArtifactManifest(ctx, repo, TarLayerMediaType,
		fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, ref.ID), tgzData, annotations)
	if err != nil {
		return nil, err
	}

	for _, tag := range tags {
		if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), tag); err != nil {
			c.log.Write(fmt.Sprintf("Failed to PUT manifest for tag %s\n%v", tag, err), log.LevelError)
			return nil, fmt.Errorf("push tag %s of %s: %w", tag, ref.Resource, err)
		}
		c.log.Write(fmt.Sprintf("Published tag: %s", tag), log.LevelInfo)
	}

	return &PushResult{
		Digest:        manifestDesc.Digest.String(),
		PublishedTags: tags,
	}, nil
}

func (c *Client) PushCollectionMetadata(ctx context.Context, ref *Ref, collectionJSONPath string) (*PushResult, error) {
	c.log.Write(fmt.Sprintf("Publishing collection metadata to %s...", ref.Resource), log.LevelInfo)

	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(collectionJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read collection metadata: %w", err)
	}

	manifestDesc, manifestBytes, err := pushArtifactManifest(ctx, repo, CollectionLayerMediaType,
		"devcontainer-collection.json", data, map[string]string{"com.github.package.type": "devcontainer_collection"})
	if err != nil {
		return nil, err
	}
	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), "latest"); err != nil {
		return nil, fmt.Errorf("push collection manifest: %w", err)
	}

	return &PushResult{Digest: manifestDesc.Digest.String(), PublishedTags: []string{"latest"}}, nil
}

func pushBlobIfAbsent(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, data []byte) error {
	if exists, err := repo.Blobs().Exists(ctx, desc); err == nil && exists {
		return nil
	}
	return repo.Blobs().Push(ctx, desc, bytes.NewReader(data))
}

func pushArtifactManifest(ctx context.Context, repo *remote.Repository, layerMediaType, title string, data []byte, annotations map[string]string) (ocispec.Descriptor, []byte, error) {
	layerDesc := ocispec.Descriptor{
		MediaType:   layerMediaType,
		Digest:      godigest.FromBytes(data),
		Size:        int64(len(data)),
		Annotations: map[string]string{"org.opencontainers.image.title": title},
	}
	if err := pushBlobIfAbsent(ctx, repo, layerDesc, data); err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("upload layer: %w", err)
	}
	configData := []byte("{}")
	configDesc := ocispec.Descriptor{MediaType: ManifestMediaType, Digest: godigest.FromBytes(configData), Size: int64(len(configData))}
	if err := pushBlobIfAbsent(ctx, repo, configDesc, configData); err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("upload config: %w", err)
	}
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     OCIManifestMediaType,
		Config:        Descriptor{Digest: configDesc.Digest.String(), MediaType: ManifestMediaType, Size: configDesc.Size},
		Layers:        []Layer{{MediaType: layerMediaType, Digest: layerDesc.Digest.String(), Size: layerDesc.Size, Annotations: layerDesc.Annotations}},
		Annotations:   annotations,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("encode manifest: %w", err)
	}
	return ocispec.Descriptor{MediaType: OCIManifestMediaType, Digest: godigest.FromBytes(manifestBytes), Size: int64(len(manifestBytes))}, manifestBytes, nil
}
