package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	orascontent "oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/devcontainers/cli/internal/httpx"
	"github.com/devcontainers/cli/internal/log"
)

var (
	responseHeaderTimeout = 30 * time.Second
	metadataOpTimeout     = 60 * time.Second
	blobOpTimeout         = 10 * time.Minute
)

type Client struct {
	log         log.Logger
	env         map[string]string
	authCache   auth.Cache
	retryClient *http.Client
	authPolicy  *AuthPolicy
}

func NewClientWithAuthPolicy(logger log.Logger, env map[string]string, policy *AuthPolicy) *Client {
	base := httpx.NewTransport()
	base.ResponseHeaderTimeout = responseHeaderTimeout
	return &Client{
		log:         logger,
		env:         env,
		authCache:   auth.NewCache(),
		retryClient: &http.Client{Transport: retry.NewTransport(base)},
		authPolicy:  policy,
	}
}

func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return true
	}
	if respErr, ok := errors.AsType[*errcode.ErrorResponse](err); ok {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}

func (c *Client) FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metadataOpTimeout)
	defer cancel()
	return c.FetchManifestContext(ctx, ref, expectedDigest)
}

func (c *Client) FetchManifestContext(ctx context.Context, ref *Ref, expectedDigest string) (*ManifestContainer, error) {
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

func (c *Client) FetchBlob(ref *Ref, digest string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), blobOpTimeout)
	defer cancel()
	return c.FetchBlobContext(ctx, ref, digest)
}

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
	return orascontent.ReadAll(rc, desc)
}

func (c *Client) GetPublishedTags(ref *Ref) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metadataOpTimeout)
	defer cancel()
	return c.GetPublishedTagsContext(ctx, ref)
}

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
