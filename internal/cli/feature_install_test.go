package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
)

func TestFetchFeatureSets(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, baseDir string)
		entries map[string]interface{}
		check   func(t *testing.T, result *fetchFeatureResult, err error)
	}{
		{
			name: "local feature parsed",
			setup: func(t *testing.T, baseDir string) {
				featureDir := filepath.Join(baseDir, "local-feature")
				if err := os.MkdirAll(featureDir, 0755); err != nil {
					t.Fatal(err)
				}
				const featureJSON = `{
			"id": "local-feature",
			"version": "1.0.0",
			"init": true,
			"customizations": {
				"vscode": {
					"extensions": ["extensionA"]
				}
			},
			"postCreateCommand": "echo hello"
		}`
				if err := os.WriteFile(filepath.Join(featureDir, "devcontainer-feature.json"), []byte(featureJSON), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(featureDir, "install.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
					t.Fatal(err)
				}
			},
			entries: map[string]interface{}{"./local-feature": map[string]interface{}{}},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if result == nil {
					t.Fatal("expected feature result")
				}
				if len(result.FeatureSets) != 1 {
					t.Fatalf("featureSets = %d, want 1", len(result.FeatureSets))
				}
				set := result.FeatureSets[0]
				src, ok := set.SourceInfo.(*features.LocalSource)
				if !ok {
					t.Fatalf("sourceInfo type = %T, want *features.LocalSource", set.SourceInfo)
				}
				if src.UserID != "./local-feature" {
					t.Fatalf("user id = %q, want ./local-feature", src.UserID)
				}
				if got := set.Features[0].Customizations["vscode"]; got == nil {
					t.Fatal("expected customizations to be parsed")
				}
				if got := set.Features[0].PostCreateCommand; got != "echo hello" {
					t.Fatalf("postCreateCommand = %#v, want %q", got, "echo hello")
				}
			},
		},
		{
			name: "resolves transitive dependsOn (with options and staging)",
			setup: func(t *testing.T, baseDir string) {
				writeLocalFeature(t, baseDir, "feature-a", map[string]interface{}{"./feature-b": map[string]interface{}{"channel": "beta"}})
				writeLocalFeature(t, baseDir, "feature-b", map[string]interface{}{"./feature-c": true})
				writeLocalFeature(t, baseDir, "feature-c", nil)
			},
			entries: map[string]interface{}{"./feature-a": map[string]interface{}{}},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if got, want := featureSetIDs(result.FeatureSets), "./feature-c → ./feature-b → ./feature-a"; got != want {
					t.Fatalf("install order = %s, want %s", got, want)
				}
				if gotOptions := result.FeatureSets[1].Features[0].UserOptions["channel"]; gotOptions != "beta" {
					t.Fatalf("dependency options = %#v, want beta", gotOptions)
				}
				for _, set := range result.FeatureSets {
					if _, err := os.Stat(set.Features[0].CachePath); err != nil {
						t.Fatalf("staged dependency %q: %v", set.SourceInfo.UserFeatureID(), err)
					}
				}
			},
		},
		{
			name: "deduplicates shared dependency",
			setup: func(t *testing.T, baseDir string) {
				writeLocalFeature(t, baseDir, "feature-a", map[string]interface{}{"./shared": true})
				writeLocalFeature(t, baseDir, "feature-b", map[string]interface{}{"./shared": true})
				writeLocalFeature(t, baseDir, "shared", nil)
			},
			entries: map[string]interface{}{"./feature-a": true, "./feature-b": true},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if len(result.FeatureSets) != 3 {
					t.Fatalf("featureSets = %d, want 3", len(result.FeatureSets))
				}
				if got := featureSetIDs(result.FeatureSets); got != "./shared → ./feature-a → ./feature-b" {
					t.Fatalf("install order = %s", got)
				}
			},
		},
		{
			name: "reports dependsOn cycle",
			setup: func(t *testing.T, baseDir string) {
				writeLocalFeature(t, baseDir, "feature-a", map[string]interface{}{"./feature-b": true})
				writeLocalFeature(t, baseDir, "feature-b", map[string]interface{}{"./feature-a": true})
			},
			entries: map[string]interface{}{"./feature-a": true},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err == nil || !strings.Contains(err.Error(), "circular dependency") {
					t.Fatalf("error = %v, want circular dependency", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			tt.setup(t, baseDir)
			result, err := fetchFeatureSets(log.Null, nil, tt.entries, baseDir, false, nil)
			if result != nil && result.TmpDir != "" {
				defer os.RemoveAll(result.TmpDir)
			}
			tt.check(t, result, err)
		})
	}
}

func writeLocalFeature(t *testing.T, baseDir, name string, dependsOn map[string]interface{}) {
	t.Helper()
	dir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]interface{}{"id": name, "version": "1.0.0"}
	if dependsOn != nil {
		metadata["dependsOn"] = dependsOn
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestFeatureMetadataEntry_SkipPersistCustomizations(t *testing.T) {
	set := &features.FeatureSet{
		SourceInfo: &features.LocalSource{UserID: "./localFeatureA"},
		Features: []features.Feature{{
			ID:                "./localFeatureA",
			Init:              boolPtr(true),
			Customizations:    map[string]interface{}{"vscode": map[string]interface{}{"extensions": []interface{}{"extensionA"}}},
			PostCreateCommand: "five",
		}},
	}

	withCustomizations := featureMetadataEntry(set, false)
	if withCustomizations.Customizations == nil {
		t.Fatal("expected customizations to be present")
	}

	withoutCustomizations := featureMetadataEntry(set, true)
	if withoutCustomizations.Customizations != nil {
		t.Fatalf("expected customizations to be omitted, got %#v", withoutCustomizations.Customizations)
	}
	if withoutCustomizations.PostCreateCommand != "five" {
		t.Fatalf("postCreateCommand = %#v, want %q", withoutCustomizations.PostCreateCommand, "five")
	}
}

func TestFeatureMetadataEntry_UsesOCIUserID(t *testing.T) {
	set := &features.FeatureSet{
		SourceInfo: &features.OCISource{UserID: "ghcr.io/devcontainers/feature-starter/hello:1"},
		Features: []features.Feature{{
			ID: "hello",
		}},
	}

	entry := featureMetadataEntry(set, false)
	if entry.ID != "ghcr.io/devcontainers/feature-starter/hello:1" {
		t.Fatalf("id = %q", entry.ID)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
