package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/product"
)

func mergeAdditionalFeatures(cfg *config.DevContainer, additionalFeaturesJSON string) (map[string]bool, error) {
	if additionalFeaturesJSON == "" {
		return nil, nil
	}

	var additional map[string]any
	if err := jsonc.Unmarshal([]byte(additionalFeaturesJSON), &additional); err != nil {
		return nil, fmt.Errorf("parse additional-features: %w", err)
	}

	if cfg.Features == nil {
		cfg.Features = make(map[string]any)
	}

	additionalOnly := make(map[string]bool)
	for k, v := range additional {
		if _, exists := cfg.Features[k]; !exists {
			cfg.Features[k] = v
			additionalOnly[k] = true
			if names := jsonc.MemberNames([]byte(additionalFeaturesJSON), k); len(names) > 0 {
				if cfg.FeatureOptionOrder == nil {
					cfg.FeatureOptionOrder = map[string][]string{}
				}
				cfg.FeatureOptionOrder[k] = names
			}
		}
	}
	return additionalOnly, nil
}

func appPortPublishArgs(raw json.RawMessage) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var items []any
	switch t := v.(type) {
	case []any:
		items = t
	default:
		items = []any{t}
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

func cliVersion() string {
	return product.Get().Version
}

func findPlatformArg(runArgs []string) string {
	for i, a := range runArgs {
		if v, ok := strings.CutPrefix(a, "--platform="); ok {
			return v
		}
		if a == "--platform" && i+1 < len(runArgs) {
			return runArgs[i+1]
		}
	}
	return ""
}
