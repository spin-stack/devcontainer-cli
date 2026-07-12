package cli

import (
	"encoding/json"
	"fmt"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/product"
	"github.com/tailscale/hujson"
)

// mergeAdditionalFeatures parses a JSON(C) string of additional features and
// merges them into cfg.Features. Config features have priority — additional
// features are only added if their key doesn't already exist.
// The returned set holds the keys that originated only from --additional-features
// (not present in config.features). These are excluded from the lockfile.
func mergeAdditionalFeatures(cfg *config.DevContainer, additionalFeaturesJSON string) (map[string]bool, error) {
	if additionalFeaturesJSON == "" {
		return nil, nil
	}

	// Parse JSONC (tolerant of comments/trailing commas)
	standardJSON, err := hujson.Standardize([]byte(additionalFeaturesJSON))
	if err != nil {
		return nil, fmt.Errorf("parse additional-features: %w", err)
	}

	var additional map[string]interface{}
	if err := json.Unmarshal(standardJSON, &additional); err != nil {
		return nil, fmt.Errorf("parse additional-features: %w", err)
	}

	if cfg.Features == nil {
		cfg.Features = make(map[string]interface{})
	}

	additionalOnly := make(map[string]bool)
	for k, v := range additional {
		if _, exists := cfg.Features[k]; !exists {
			cfg.Features[k] = v
			additionalOnly[k] = true
		}
	}
	return additionalOnly, nil
}

// appPortPublishArgs turns the config `appPort` (number | string | array) into
// `docker create` publish flags, matching the TS CLI: a number N publishes
// 127.0.0.1:N:N, a string is used verbatim (e.g. "8080:80").
func appPortPublishArgs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var items []interface{}
	switch t := v.(type) {
	case []interface{}:
		items = t
	default:
		items = []interface{}{t}
	}
	var args []string
	for _, item := range items {
		switch p := item.(type) {
		case float64:
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", int(p), int(p)))
		case string:
			if p != "" {
				args = append(args, "-p", p)
			}
		}
	}
	return args
}

// cliVersion returns the CLI version for the log banner (set via ldflags).
func cliVersion() string {
	return product.Get().Version
}

// logDimensions returns Dimensions for the logger if terminal size was provided.
func logDimensions(columns, rows int) *log.Dimensions {
	if columns > 0 || rows > 0 {
		return &log.Dimensions{Columns: columns, Rows: rows}
	}
	return nil
}
