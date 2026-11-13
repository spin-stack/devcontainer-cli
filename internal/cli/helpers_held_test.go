package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

func TestMergeAdditionalFeatures(t *testing.T) {
	cfg := &config.DevContainer{Features: map[string]any{"ghcr.io/a/b/node:1": map[string]any{"version": "20"}}}
	only, err := mergeAdditionalFeatures(cfg, `{
		// JSONC is accepted
		"ghcr.io/a/b/node:1": {"version": "18"},
		"ghcr.io/a/b/go:1": {}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"ghcr.io/a/b/go:1": true}; !maps.Equal(only, want) {
		t.Errorf("additional-only = %v, want %v", only, want)
	}
	if v := cfg.Features["ghcr.io/a/b/node:1"].(map[string]any)["version"]; v != "20" {
		t.Errorf("config value overridden: version = %v, want 20", v)
	}
	if _, ok := cfg.Features["ghcr.io/a/b/go:1"]; !ok {
		t.Errorf("additional Feature not merged: %v", cfg.Features)
	}

	empty := &config.DevContainer{}
	if _, err := mergeAdditionalFeatures(empty, `{"ghcr.io/a/b/go:1": {}}`); err != nil {
		t.Fatal(err)
	}
	if len(empty.Features) != 1 {
		t.Errorf("features = %v, want the additional Feature on a config without features", empty.Features)
	}
}

func TestMergeAdditionalFeaturesKeepsTheOptionOrder(t *testing.T) {
	cfg := &config.DevContainer{
		Features:           map[string]any{"./f": map[string]any{"b": 1, "a": 2}},
		FeatureOptionOrder: map[string][]string{"./f": {"b", "a"}},
	}
	if _, err := mergeAdditionalFeatures(cfg, `{"./f": {"x": 1}, "./g": {"zz": 1, "aa": 2}, "./h": true}`); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"./f": {"b", "a"}, "./g": {"zz", "aa"}}
	if len(cfg.FeatureOptionOrder) != len(want) {
		t.Errorf("FeatureOptionOrder = %v, want %v", cfg.FeatureOptionOrder, want)
	}
	for id, names := range want {
		if !slices.Equal(cfg.FeatureOptionOrder[id], names) {
			t.Errorf("FeatureOptionOrder[%s] = %v, want %v", id, cfg.FeatureOptionOrder[id], names)
		}
	}
}

func TestMergeAdditionalFeaturesRejectsInvalidJSON(t *testing.T) {
	cfg := &config.DevContainer{}
	if _, err := mergeAdditionalFeatures(cfg, `{"x":`); err == nil {
		t.Fatal("want a parse error for malformed --additional-features")
	}
	if cfg.Features != nil {
		t.Errorf("features = %v, want the config untouched on a parse error", cfg.Features)
	}
}
