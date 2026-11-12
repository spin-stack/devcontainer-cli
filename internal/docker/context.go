package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

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
