package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orascontent "oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/devcontainers/cli/internal/log"
)

// dockerManifestListMediaType is the Docker counterpart of an OCI image index.
const dockerManifestListMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"

// ImageConfig is the part of an image's config blob ("config" object) the CLI
// reads: the default user, environment and labels (devcontainer.metadata).
type ImageConfig struct {
	User   string            `json:"User,omitempty"`
	Env    []string          `json:"Env,omitempty"`
	Labels map[string]string `json:"Labels,omitempty"`
}

// ImageDetails is the TS ImageDetails built by inspectImageInRegistry
// (utils.ts): the config digest as Id, the image config, and the platform that
// was asked for.
type ImageDetails struct {
	ID           string
	Config       ImageConfig
	OS           string
	Architecture string
	Variant      string
}

// QualifyImageName is TS qualifyImageName (utils.ts): a bare name is a Docker
// Hub library image, a two-segment name is a Docker Hub user image, anything
// longer already names its registry.
func QualifyImageName(name string) string {
	segments := strings.Split(name, "/")
	switch len(segments) {
	case 1:
		return "docker.io/library/" + name
	case 2:
		if strings.HasPrefix(name, "docker.io/") {
			return "docker.io/library/" + segments[1]
		}
		return "docker.io/" + name
	default:
		return name
	}
}

// registryServer is the host the registry API is served from: Docker Hub's
// API lives on registry-1.docker.io, not docker.io (TS inspectImageInRegistry).
func registryServer(registry string) string {
	if registry == "docker.io" {
		return "registry-1.docker.io"
	}
	return registry
}

// InspectImageInRegistry is TS inspectImageInRegistry (utils.ts): it reads an
// image's config straight from its registry, without pulling it. The reference
// resolves to an image manifest (its config is used whatever its platform, as
// TS does) or to an image index / manifest list, whose entry for platform is
// then fetched. The config blob's "config" object becomes ImageDetails.Config.
func (c *Client) InspectImageInRegistry(ctx context.Context, name string, platform ocispec.Platform) (*ImageDetails, error) {
	resourceAndVersion := QualifyImageName(name)
	ref, err := ParseRef(resourceAndVersion)
	if err != nil {
		return nil, fmt.Errorf("Could not parse image name '%s'", name)
	}
	ctx, cancel := context.WithTimeout(ctx, metadataOpTimeout)
	defer cancel()

	server := ref
	if s := registryServer(ref.Registry); s != ref.Registry {
		cp := *ref
		cp.Registry = s
		server = &cp
	}
	repo, err := c.repository(server)
	if err != nil {
		return nil, err
	}
	if server != ref {
		// Credentials are looked up for the registry as named (docker.io), like
		// TS requestEnsureAuthenticated does with ref.registry.
		if ac, ok := repo.Client.(*auth.Client); ok {
			named, credential := ref.Registry, ac.Credential
			ac.Credential = func(ctx context.Context, host string) (auth.Credential, error) {
				if host == server.Registry {
					host = named
				}
				return credential(ctx, host)
			}
		}
	}
	c.log.Write(fmt.Sprintf("manifest url: %s://%s/v2/%s/manifests/%s", ref.Scheme(), server.Registry, ref.Path, ref.Version), log.LevelTrace)

	noManifest := fmt.Errorf("No manifest found for %s.", resourceAndVersion)
	desc, body, err := fetchManifestBytes(ctx, repo, ref.Version)
	if err != nil {
		if IsNotFound(err) {
			return nil, noManifest
		}
		return nil, fmt.Errorf("fetch manifest for %s: %w", resourceAndVersion, err)
	}

	var configDesc ocispec.Descriptor
	switch desc.MediaType {
	case ocispec.MediaTypeImageIndex, dockerManifestListMediaType:
		var index ocispec.Index
		if err := json.Unmarshal(body, &index); err != nil {
			return nil, fmt.Errorf("parse image index for %s: %w", resourceAndVersion, err)
		}
		entry, ok := indexEntryForPlatform(index, platform)
		if !ok {
			return nil, noManifest
		}
		_, body, err = fetchManifestBytes(ctx, repo, entry.Digest.String())
		if err != nil {
			if IsNotFound(err) {
				return nil, noManifest
			}
			return nil, fmt.Errorf("fetch manifest for %s: %w", resourceAndVersion, err)
		}
		if configDesc, err = manifestConfig(body); err != nil {
			return nil, fmt.Errorf("parse manifest for %s: %w", resourceAndVersion, err)
		}
	default:
		if configDesc, err = manifestConfig(body); err != nil {
			return nil, fmt.Errorf("parse manifest for %s: %w", resourceAndVersion, err)
		}
	}
	if configDesc.Digest == "" {
		return nil, noManifest
	}

	c.log.Write(fmt.Sprintf("blob url: %s://%s/v2/%s/blobs/%s", ref.Scheme(), server.Registry, ref.Path, configDesc.Digest), log.LevelTrace)
	rc, err := repo.Fetch(ctx, configDesc)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch blob for %s: %w", resourceAndVersion, err)
	}
	defer rc.Close()
	blob, err := orascontent.ReadAll(rc, configDesc)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch blob for %s: %w", resourceAndVersion, err)
	}
	var cfg struct {
		Config ImageConfig `json:"config"`
	}
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return nil, fmt.Errorf("parse image config for %s: %w", resourceAndVersion, err)
	}
	return &ImageDetails{
		ID:           configDesc.Digest.String(),
		Config:       cfg.Config,
		OS:           platform.OS,
		Architecture: platform.Architecture,
		Variant:      platform.Variant,
	}, nil
}

// fetchManifestBytes fetches a manifest by tag or digest and reads it, verifying
// the bytes against the returned descriptor.
func fetchManifestBytes(ctx context.Context, repo *remote.Repository, reference string) (ocispec.Descriptor, []byte, error) {
	desc, rc, err := repo.FetchReference(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer rc.Close()
	body, err := orascontent.ReadAll(rc, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, body, nil
}

// manifestConfig returns the config descriptor of an image manifest (OCI or
// Docker v2 schema 2); it is empty when the document has none.
func manifestConfig(body []byte) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return ocispec.Descriptor{}, err
	}
	return m.Config, nil
}

// indexEntryForPlatform is TS getImageIndexEntryForPlatform
// (containerCollectionsOCI.ts): the first entry whose os and architecture match,
// and whose variant matches when a variant is asked for.
func indexEntryForPlatform(index ocispec.Index, platform ocispec.Platform) (ocispec.Descriptor, bool) {
	for _, m := range index.Manifests {
		if m.Platform == nil || m.Platform.Architecture != platform.Architecture || m.Platform.OS != platform.OS {
			continue
		}
		if platform.Variant == "" || m.Platform.Variant == platform.Variant {
			return m, true
		}
	}
	return ocispec.Descriptor{}, false
}
