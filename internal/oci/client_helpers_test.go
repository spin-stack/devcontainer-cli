package oci

import "github.com/devcontainers/cli/internal/log"

// NewClient is the tests' shorthand for a client with the default auth policy;
// the CLI always builds one with the invocation's policy (NewClientWithAuthPolicy).
func NewClient(logger log.Logger, env map[string]string) *Client {
	return NewClientWithAuthPolicy(logger, env, DefaultAuthPolicy())
}
