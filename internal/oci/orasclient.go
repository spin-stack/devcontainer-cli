package oci

import (
	"context"
	"encoding/base64"
	"strings"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// isLocalRegistry reports whether the registry is a local/insecure one that
// serves plain HTTP (localhost / loopback), as opposed to a real HTTPS registry.
func isLocalRegistry(registry string) bool {
	host := registry
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// repository builds an oras-go remote.Repository for the given ref, wired to the
// devcontainer credential resolution (DEVCONTAINERS_OCI_AUTH → docker config /
// credential helpers → GITHUB_TOKEN → platform default → anonymous) and the
// package's default retrying HTTP client. This is the transport/auth engine
// behind FetchManifest/FetchBlob/GetPublishedTags; it handles bearer-token scope
// negotiation (including push) and retries.
func (c *Client) repository(ref *Ref) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.Registry + "/" + ref.Path)
	if err != nil {
		return nil, err
	}
	// Local/insecure registries (localhost, 127.0.0.1) speak plain HTTP.
	repo.PlainHTTP = isLocalRegistry(ref.Registry)
	// Reuse the client-level auth cache across operations. Fall back to a
	// per-repository cache if the client was constructed without one (e.g. a
	// zero-value Client in a test), so repository() never wires a nil cache.
	cache := c.authCache
	if cache == nil {
		cache = auth.NewCache()
	}
	// Use the shared proxy/CA-aware retrying client. Fall back to the oras default
	// for a zero-value Client (e.g. a test that constructs &Client{} directly).
	httpClient := c.retryClient
	if httpClient == nil {
		httpClient = retry.DefaultClient
	}
	repo.Client = &auth.Client{
		Client: httpClient,
		Cache:  cache,
		Credential: func(_ context.Context, host string) (auth.Credential, error) {
			cred := getCredential(c.env, host, c.log)
			if cred == nil {
				return auth.EmptyCredential, nil
			}
			ac := auth.Credential{RefreshToken: cred.refreshToken}
			if cred.base64Encoded != "" {
				if raw, decErr := base64.StdEncoding.DecodeString(cred.base64Encoded); decErr == nil {
					if user, pass, ok := strings.Cut(string(raw), ":"); ok {
						ac.Username = user
						ac.Password = pass
					}
				}
			}
			return ac, nil
		},
	}
	return repo, nil
}
