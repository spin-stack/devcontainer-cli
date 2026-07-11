package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/features"
)

func TestFetchFeatureSets_LocalFeature(t *testing.T) {
	baseDir := t.TempDir()
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

	result, err := fetchFeatureSets(log.Null, map[string]interface{}{
		"./local-feature": map[string]interface{}{},
	}, baseDir, false, nil)
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
	if src.ResolvedPath != filepath.Clean(featureDir) {
		t.Fatalf("resolved path = %q, want %q", src.ResolvedPath, filepath.Clean(featureDir))
	}
	if got := set.Features[0].Customizations["vscode"]; got == nil {
		t.Fatal("expected customizations to be parsed")
	}
	if got := set.Features[0].PostCreateCommand; got != "echo hello" {
		t.Fatalf("postCreateCommand = %#v, want %q", got, "echo hello")
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
