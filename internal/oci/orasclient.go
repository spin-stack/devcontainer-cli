package oci

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func (c *Client) repository(ref *Ref) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.Registry + "/" + ref.Path)
	if err != nil {
		return nil, err
	}
	repo.PlainHTTP = ref.Scheme() == "http"
	cache := c.authCache
	if cache == nil {
		cache = auth.NewCache()
	}
	httpClient := c.retryClient
	if httpClient == nil {
		httpClient = retry.DefaultClient
	}
	policy := c.authPolicy
	if policy == nil {
		policy = DefaultAuthPolicy()
	}
	scheme := "https"
	if repo.PlainHTTP {
		scheme = "http"
	}
	baseTransport := httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	policyClient := *httpClient
	policyClient.Transport = newAuthPolicyTransport(baseTransport, policy, scheme, ref.Registry)
	httpClient = &policyClient
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
