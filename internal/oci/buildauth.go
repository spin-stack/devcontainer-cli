package oci

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/log"
)

func ResolveBuildAuth(env map[string]string, registries []string, logger log.Logger) (dir string, cleanup func(), ok bool, err error) {
	noop := func() {}

	seen := map[string]bool{}
	auths := map[string]dockerConfigAuth{}
	for _, reg := range registries {
		if reg == "" || seen[reg] {
			continue
		}
		seen[reg] = true
		cred := getCredential(env, reg, logger)
		if cred == nil || (cred.base64Encoded == "" && cred.refreshToken == "") {
			continue
		}
		entry := dockerConfigAuth{Auth: cred.base64Encoded}
		if cred.refreshToken != "" {
			entry.IdentityToken = cred.refreshToken
		}
		auths[reg] = entry
	}
	if len(auths) == 0 {
		return "", noop, false, nil
	}

	dir, err = os.MkdirTemp("", "devcontainer-buildauth-")
	if err != nil {
		return "", noop, false, err
	}
	data, err := json.MarshalIndent(struct {
		Auths map[string]dockerConfigAuth `json:"auths"`
	}{Auths: auths}, "", "  ")
	if err != nil {
		os.RemoveAll(dir)
		return "", noop, false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", noop, false, err
	}
	logger.Write("[buildAuth] wrote temporary DOCKER_CONFIG for the build subprocess", log.LevelTrace)
	return dir, func() { os.RemoveAll(dir) }, true, nil
}
