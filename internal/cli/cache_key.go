package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/features"
)

// proxyEnvKeys are the proxy-related environment variables folded into the cache
// key: they materially change what a build produces (proxies baked into package
// installs), so two otherwise-identical configs built behind different proxies
// must get different keys.
var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "FTP_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "ftp_proxy", "all_proxy",
}

// cacheKeyInput is the canonical, deterministic pre-image of the cache key. Field
// order is fixed and every nested map is marshaled by encoding/json with sorted
// keys, so the same inputs always hash to the same value.
type cacheKeyInput struct {
	Config     json.RawMessage   `json:"config"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Lockfile   json.RawMessage   `json:"lockfile,omitempty"`
	Proxy      map[string]string `json:"proxy,omitempty"`
}

// computeCacheKey returns a deterministic, content-addressed cache key for a
// resolved configuration: sha256 over the normalized devcontainer.json, the
// Dockerfile (when Dockerfile-based), the features lockfile (when present, which
// pins resolved digests), the build context path, and the proxy environment.
//
// It is hermetic — no network, no registry resolution — so the same inputs on
// any host yield the same key. Feature refs are hashed as declared; pin them (via
// @sha256 or a committed lockfile) for the key to track the exact feature bits.
func computeCacheKey(result *config.LoadResult, env map[string]string) (string, error) {
	cfg := result.Config
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	in := cacheKeyInput{Config: cfgJSON, Context: cfg.GetBuildContext()}

	configDir := filepath.Dir(cfg.ConfigFilePath)
	if df := cfg.GetDockerfile(); df != "" {
		if content, rerr := os.ReadFile(filepath.Join(configDir, df)); rerr == nil {
			in.Dockerfile = string(content)
		}
	}

	if lock, ok, _ := features.ReadLockfile(cfg.ConfigFilePath); ok && lock != nil {
		if lockJSON, lerr := json.Marshal(lock); lerr == nil {
			in.Lockfile = lockJSON
		}
	}

	proxy := map[string]string{}
	for _, k := range proxyEnvKeys {
		if v, ok := env[k]; ok && v != "" {
			proxy[k] = v
		}
	}
	if len(proxy) > 0 {
		in.Proxy = proxy
	}

	preimage, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(preimage)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// proxyEnvFromEnviron snapshots only the proxy-related variables from the process
// environment (keeps computeCacheKey injectable in tests).
func proxyEnvFromEnviron() map[string]string {
	m := map[string]string{}
	for _, k := range proxyEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	return m
}
