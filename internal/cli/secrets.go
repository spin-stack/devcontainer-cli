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
