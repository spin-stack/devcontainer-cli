package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/features"
)

var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "FTP_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "ftp_proxy", "all_proxy",
}

type cacheKeyInput struct {
	Config     json.RawMessage   `json:"config"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Lockfile   json.RawMessage   `json:"lockfile,omitempty"`
	Proxy      map[string]string `json:"proxy,omitempty"`
}

func computeCacheKey(result *config.LoadResult, env map[string]string) (string, error) {
	cfg := result.Config
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	in := cacheKeyInput{Config: cfgJSON, Context: cfg.BuildContext()}

	if cfg.Dockerfile() != "" {
		content, err := os.ReadFile(dockerfileConfigPath(cfg))
		if err != nil {
			return "", fmt.Errorf("read Dockerfile: %w", err)
		}
		in.Dockerfile = string(content)
	}

	lock, _, err := features.ReadLockfile(cfg.ConfigFilePath)
	if err != nil {
		return "", fmt.Errorf("read features lockfile %s: %w", features.LockfilePath(cfg.ConfigFilePath), err)
	}
	if lock != nil {
		lockJSON, err := json.Marshal(lock)
		if err != nil {
			return "", err
		}
		in.Lockfile = lockJSON
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

func proxyEnvFromEnviron() map[string]string {
	m := map[string]string{}
	for _, k := range proxyEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	return m
}
