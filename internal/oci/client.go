package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	orascontent "oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/devcontainers/cli/internal/httpx"
	"github.com/devcontainers/cli/internal/log"
)

// Client performs OCI registry operations (pull manifest, download blob, list
// tags, push artifacts) on top of oras-go.
type Client struct {
	log log.Log
	env map[string]string
	// authCache is shared across every repository() built by this client so that
	// a bearer token / auth challenge negotiated for one operation (e.g. a push)
	// is reused by related operations (e.g. the tag list or manifest fetch that
	// follows) instead of re-running the 401→WWW-Authenticate→token loop each
	// time. auth.Cache is safe for concurrent use.
	authCache auth.Cache
	// retryClient is the oras retrying HTTP client wired to the shared transport
	// (httpx.NewTransport): it honors HTTP(S)_PROXY/NO_PROXY and loads extra CA
	// certs (NODE_EXTRA_CA_CERTS/SSL_CERT_FILE), so registry access behaves like
	// the plain httpx path — including behind a TLS-intercepting proxy.
	retryClient *http.Client
}

// NewClient creates an OCI client. Auth and retries are handled by oras-go (see
// repository()); the HTTP transport is the shared proxy/CA-aware transport.
func NewClient(logger log.Log, env map[string]string) *Client {
	return &Client{
		log:         logger,
		env:         env,
		authCache:   auth.NewCache(),
		retryClient: &http.Client{Transport: retry.NewTransport(httpx.NewTransport())},
	}
}

// FetchManifest fetches and validates an OCI manifest using oras-go for
// transport/auth (bearer-token scope negotiation, retries), keeping the
// devcontainer-specific validation (config media type must be
// application/vnd.devcontainers) and result shape.
func (c *Client) FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error) {
	return c.FetchManifestContext(context.Background(), ref, expectedDigest)
}

// FetchManifestContext is FetchManifest with caller-controlled cancellation.
func (c *Client) FetchManifestContext(ctx context.Context, ref *Ref, expectedDigest string) (*ManifestContainer, error) {
	// Skip non-domain registries
	if !strings.Contains(ref.Registry, ".") && !strings.HasPrefix(ref.Registry, "localhost") {
		return nil, fmt.Errorf("registry %q does not look like a domain", ref.Registry)
	}

	reference := ref.Version
	if expectedDigest != "" {
		reference = expectedDigest
	}

	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}

	c.log.Write(fmt.Sprintf("manifest url: https://%s/v2/%s/manifests/%s", ref.Registry, ref.Path, reference), log.LevelTrace)

	desc, rc, err := repo.FetchReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// content.ReadAll verifies the bytes against desc.Digest and desc.Size.
	body, err := orascontent.ReadAll(rc, desc)
	if err != nil {
		return nil, err
	}
	contentDigest := desc.Digest.String()

	if expectedDigest != "" && contentDigest != expectedDigest {
		return nil, fmt.Errorf("digest mismatch for %s: got %s, want %s", ref.Resource, contentDigest, expectedDigest)
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if manifest.Config.MediaType != ManifestMediaType {
		return nil, fmt.Errorf("unexpected manifest media type: %s", manifest.Config.MediaType)
	}

	return &ManifestContainer{
		Manifest:      &manifest,
		ManifestBytes: body,
		ContentDigest: contentDigest,
		CanonicalID:   fmt.Sprintf("%s@%s", ref.Resource, contentDigest),
	}, nil
}

// FetchBlob downloads a blob and verifies its digest (via oras-go).
func (c *Client) FetchBlob(ref *Ref, digest string) ([]byte, error) {
	return c.FetchBlobContext(context.Background(), ref, digest)
}

// FetchBlobContext is FetchBlob with caller-controlled cancellation.
func (c *Client) FetchBlobContext(ctx context.Context, ref *Ref, digest string) ([]byte, error) {
	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}
	desc, rc, err := repo.Blobs().FetchReference(ctx, digest)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// content.ReadAll verifies the bytes against desc.Digest and desc.Size.
	return orascontent.ReadAll(rc, desc)
}

// GetPublishedTags lists all tags for a resource (via oras-go).
func (c *Client) GetPublishedTags(ref *Ref) ([]string, error) {
	return c.GetPublishedTagsContext(context.Background(), ref)
}

// GetPublishedTagsContext is GetPublishedTags with caller-controlled cancellation.
func (c *Client) GetPublishedTagsContext(ctx context.Context, ref *Ref) ([]string, error) {
	repo, err := c.repository(ref)
	if err != nil {
		return nil, err
	}
	var tags []string
	if err := repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return nil, err
	}
	return tags, nil
}
