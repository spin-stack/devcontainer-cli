package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// readSecretsFile reads a JSON file {"KEY": "VALUE", ...} and returns ["KEY=VALUE", ...].
func readSecretsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	var envs []string
	for k, v := range secrets {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	return envs, nil
}

// secretValuesFromFile returns just the secret values from a secrets file, for
// log masking. Best-effort: returns nil on any error (the eventual load of the
// same file surfaces real problems).
func secretValuesFromFile(path string) []string {
	if path == "" {
		return nil
	}
	envs, err := readSecretsFile(path)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(envs))
	for _, e := range envs {
		if i := strings.IndexByte(e, '='); i >= 0 {
			values = append(values, e[i+1:])
		}
	}
	return values
}

// buildSecretsFromFile reads build secrets ("KEY=VALUE") from a --secrets-file
// path, or nil when unset/unreadable (a read error is non-fatal — the build
// proceeds without secrets, matching the main build path).
func buildSecretsFromFile(path string) []string {
	if path == "" {
		return nil
	}
	secrets, err := readSecretsFile(path)
	if err != nil {
		return nil
	}
	return secrets
}

// buildSecretIDs extracts the secret id (the KEY of each "KEY=VALUE") so it can
// be mounted into a feature-install RUN as --mount=type=secret,id=KEY.
func buildSecretIDs(secrets []string) []string {
	ids := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if i := strings.IndexByte(s, '='); i > 0 {
			ids = append(ids, s[:i])
		}
	}
	return ids
}
