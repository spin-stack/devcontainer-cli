package oci

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/log"
)

// ResolveBuildAuth resolves credentials for the given registries using the same
// chain as the CLI's own registry operations (DEVCONTAINERS_OCI_AUTH → docker
// config / credential helpers → GITHUB_TOKEN) and, when any resolve, writes a
// self-contained temporary docker config.json suitable for a `docker build`
// subprocess. Callers point DOCKER_CONFIG at the returned directory.
//
// The temp config carries ONLY the resolved `auths` (no credsStore/credHelpers):
// getCredential already materializes tokens from every source — including the
// user's credential store — so a static auths entry both wins Docker's lookup
// precedence and covers store-backed registries, without inheriting a credsStore
// that would otherwise shadow the injected entries.
//
// ok is false (dir "", no-op cleanup) when no credentials resolved; the caller
// then leaves the build's ambient auth untouched — so this never regresses a
// build that already authenticates via the environment.
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
		// Skip only when there is no usable credential at all. A token-only
		// credential (identitytoken / OAuth refresh token, e.g. ACR) has an empty
		// base64Encoded but a non-empty refreshToken — the old `base64Encoded == ""`
		// guard dropped it, so the build subprocess authenticated anonymously and
		// the base-image pull failed even though the CLI's own auth worked.
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
