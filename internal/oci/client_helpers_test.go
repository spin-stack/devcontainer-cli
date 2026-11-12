package oci

import "github.com/devcontainers/cli/internal/log"

func NewClient(logger log.Logger, env map[string]string) *Client {
	return NewClientWithAuthPolicy(logger, env, DefaultAuthPolicy())
}
