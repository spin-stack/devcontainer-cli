package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// resolveDockerContextHost returns the Docker endpoint host of the active Docker
// CLI context by reading the context store directly — no `docker` binary — matching
// the Docker CLI's own resolution order:
//
//	DOCKER_CONTEXT env → ~/.docker/config.json "currentContext" → "default"
//
// The "default" context has no stored endpoint, so "" is returned and the caller
// falls back to the SDK's env-based default socket. It is only consulted when
// DOCKER_HOST is unset (that rung forces "default"), so DOCKER_HOST/-H handling
// lives in the caller.
func resolveDockerContextHost() string {
	configDir := dockerConfigDir()
	name := activeContextName(configDir)
	if name == "" || name == "default" {
		return ""
	}
	// The per-context meta directory is the hex-encoded SHA-256 of the context
	// name (github.com/docker/cli store.contextdirOf → digest.FromString(name)).
	sum := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Endpoints.Docker.Host
}

// dockerConfigDir returns the Docker CLI config directory: $DOCKER_CONFIG, else
// ~/.docker.
func dockerConfigDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// activeContextName resolves the active context name: $DOCKER_CONTEXT, else the
// "currentContext" field of config.json, else "default".
func activeContextName(configDir string) string {
	if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
		return c
	}
	if configDir != "" {
		if data, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
			var cfg struct {
				CurrentContext string `json:"currentContext"`
			}
			if json.Unmarshal(data, &cfg) == nil && cfg.CurrentContext != "" {
				return cfg.CurrentContext
			}
		}
	}
	return "default"
}
