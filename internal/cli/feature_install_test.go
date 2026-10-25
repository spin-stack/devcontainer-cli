package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func TestFetchFeatureSets(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, baseDir string)
		entries map[string]any
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
			entries: map[string]any{"./local-feature": map[string]any{}},
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
				writeLocalFeature(t, baseDir, "feature-a", map[string]any{"./feature-b": map[string]any{"channel": "beta"}})
				writeLocalFeature(t, baseDir, "feature-b", map[string]any{"./feature-c": true})
				writeLocalFeature(t, baseDir, "feature-c", nil)
			},
			entries: map[string]any{"./feature-a": map[string]any{}},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				// TS: a local Feature's id is its devcontainer-feature.json id.
				if got, want := featureSetIDs(result.FeatureSets), "feature-c → feature-b → feature-a"; got != want {
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
				writeLocalFeature(t, baseDir, "feature-a", map[string]any{"./shared": true})
				writeLocalFeature(t, baseDir, "feature-b", map[string]any{"./shared": true})
				writeLocalFeature(t, baseDir, "shared", nil)
			},
			entries: map[string]any{"./feature-a": true, "./feature-b": true},
			check: func(t *testing.T, result *fetchFeatureResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if len(result.FeatureSets) != 3 {
					t.Fatalf("featureSets = %d, want 3", len(result.FeatureSets))
				}
				if got := featureSetIDs(result.FeatureSets); got != "shared → feature-a → feature-b" {
					t.Fatalf("install order = %s", got)
				}
			},
		},
		{
			name: "reports dependsOn cycle",
			setup: func(t *testing.T, baseDir string) {
				writeLocalFeature(t, baseDir, "feature-a", map[string]any{"./feature-b": true})
				writeLocalFeature(t, baseDir, "feature-b", map[string]any{"./feature-a": true})
			},
			entries: map[string]any{"./feature-a": true},
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
			result, err := fetchFeatureSets(t.Context(), log.Null, tt.entries, baseDir, false)
			if result != nil && result.TmpDir != "" {
				defer os.RemoveAll(result.TmpDir)
			}
			tt.check(t, result, err)
		})
	}
}

func writeLocalFeature(t *testing.T, baseDir, name string, dependsOn map[string]any) {
	t.Helper()
	dir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"id": name, "version": "1.0.0"}
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
	set := &features.Set{
		SourceInfo: &features.LocalSource{UserID: "./localFeatureA"},
		Features: []features.Feature{{
			ID:                "./localFeatureA",
			Init:              new(true),
			Customizations:    map[string]any{"vscode": map[string]any{"extensions": []any{"extensionA"}}},
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
	set := &features.Set{
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

// TestFetchFeatureSets_StagingLayout: like TS generateFeaturesConfig, the
// Features are staged in <cacheFolder>/container-features/<cliVersion>-<ms>,
// each in a folder named by its consecutiveId `<feature id>_<install index>`,
// and no staging leftovers remain. A local Feature's id is its
// devcontainer-feature.json id, else the folder name; its name, when the file
// has none, is the user's reference.
func TestFetchFeatureSets_StagingLayout(t *testing.T) {
	baseDir := t.TempDir()
	writeLocalFeature(t, baseDir, "dir-a", map[string]any{"./dir-b": true})
	dirB := filepath.Join(baseDir, "dir-b")
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "devcontainer-feature.json"), []byte(`{"version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := fetchFeatureSets(t.Context(), log.Null, map[string]any{"./dir-a": true}, baseDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(result.TmpDir)

	parent := filepath.Join(cacheFolder(), "container-features")
	if filepath.Dir(result.TmpDir) != parent {
		t.Fatalf("features folder = %q, want a folder of %q", result.TmpDir, parent)
	}
	prefix := cliVersion() + "-"
	base := filepath.Base(result.TmpDir)
	if !strings.HasPrefix(base, prefix) || strings.Trim(strings.TrimPrefix(base, prefix), "0123456789") != "" {
		t.Fatalf("features folder name = %q, want %s<ms>", base, prefix)
	}
	want := []struct{ id, consecutiveID, name string }{
		{"dir-b", "dir-b_0", "./dir-b"},
		{"dir-a", "dir-a_1", "./dir-a"},
	}
	if len(result.FeatureSets) != len(want) {
		t.Fatalf("featureSets = %d, want %d", len(result.FeatureSets), len(want))
	}
	for i, w := range want {
		f := result.FeatureSets[i].Features[0]
		if f.ID != w.id || f.ConsecutiveId != w.consecutiveID || f.Name != w.name {
			t.Errorf("feature %d = id %q consecutiveId %q name %q, want %q %q %q", i, f.ID, f.ConsecutiveId, f.Name, w.id, w.consecutiveID, w.name)
		}
		if f.CachePath != filepath.Join(result.TmpDir, w.consecutiveID) {
			t.Errorf("feature %d cachePath = %q", i, f.CachePath)
		}
		if _, err := os.Stat(filepath.Join(f.CachePath, "install.sh")); err != nil {
			t.Errorf("feature %d not staged: %v", i, err)
		}
	}
	entries, err := os.ReadDir(result.TmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("features folder holds %v, want only the Features' folders", names)
	}
}

// TestStageFeatureBuildFiles: the files the features Dockerfile refers to are
// written as TS getFeaturesBuildOptions writes them: the builtin env in the
// features folder, and the env file and install wrapper (options in
// devcontainer-feature.json order) in each Feature's folder.
func TestStageFeatureBuildFiles(t *testing.T) {
	dst := t.TempDir()
	cache := filepath.Join(dst, "hello_0")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "devcontainer-feature.json"), []byte(`{
		// JSONC
		"id": "hello", "name": "Hello", "version": "1.0.0",
		"options": {"zeta": {"type": "string", "default": "z"}, "alpha": {"type": "boolean", "default": true}},
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	set := &features.Set{
		SourceInfo: &features.OCISource{UserID: "ghcr.io/o/f/hello:1", UserFeatureIdWithoutVersion: "ghcr.io/o/f/hello"},
		Features: []features.Feature{{
			ID: "hello", Name: "Hello", Version: "1.0.0", ConsecutiveId: "hello_0", CachePath: cache,
			Value:   map[string]any{"alpha": false},
			Options: map[string]any{"zeta": map[string]any{"default": "z"}, "alpha": map[string]any{"default": true}},
		}},
	}
	if err := stageFeatureBuildFiles(dst, []*features.Set{set}, "root", "vscode"); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := read(filepath.Join(dst, "devcontainer-features.builtin.env")); got != "_CONTAINER_USER=root\n_REMOTE_USER=vscode\n" {
		t.Errorf("builtin env = %q", got)
	}
	if got := read(filepath.Join(cache, "devcontainer-features.env")); got != "ZETA=\"z\"\nALPHA=\"false\"" {
		t.Errorf("feature env = %q", got)
	}
	wrapper := read(filepath.Join(cache, "devcontainer-features-install.sh"))
	for _, want := range []string{"echo 'Feature       : Hello'\n", "echo 'Id            : ghcr.io/o/f/hello'\n", "echo '    ZETA=\"z\"\n    ALPHA=\"false\"'\n", ". ../devcontainer-features.builtin.env\n. ./devcontainer-features.env\n"} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("install wrapper missing %q:\n%s", want, wrapper)
		}
	}

	// A Feature folder without devcontainer-feature.json is an error, not an
	// install with no options.
	if err := os.Remove(filepath.Join(cache, "devcontainer-feature.json")); err != nil {
		t.Fatal(err)
	}
	if err := stageFeatureBuildFiles(dst, []*features.Set{set}, "root", "vscode"); err == nil {
		t.Error("missing devcontainer-feature.json must fail")
	}
}

// TS getFeatureIdWithoutVersion strips the last :tag or @digest, keeping a
// registry port.
func TestFeatureIDWithoutVersion(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/devcontainers/features/node:1":         "ghcr.io/devcontainers/features/node",
		"ghcr.io/devcontainers/features/node@sha256:ab": "ghcr.io/devcontainers/features/node",
		"localhost:5000/o/f/node":                       "localhost:5000/o/f/node",
		"localhost:5000/o/f/node:2":                     "localhost:5000/o/f/node",
	} {
		if got := featureIDWithoutVersion(in); got != want {
			t.Errorf("featureIDWithoutVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TS getDevcontainerMetadata records sourceInformation.userFeatureId as a
// Feature entry's id for every source type, not the Feature's own id.
func TestFeatureMetadataEntry_UsesUserFeatureIDForLocalAndTarball(t *testing.T) {
	local := featureMetadataEntry(&features.Set{SourceInfo: &features.LocalSource{UserID: "./hello"}, Features: []features.Feature{{ID: "hello"}}}, false)
	if local.ID != "./hello" {
		t.Errorf("local id = %q", local.ID)
	}
	tarball := featureMetadataEntry(&features.Set{SourceInfo: &features.TarballSource{TarballURI: "https://x/devcontainer-feature-hello.tgz", UserID: "https://x/devcontainer-feature-hello.tgz"}, Features: []features.Feature{{ID: "hello"}}}, false)
	if tarball.ID != "https://x/devcontainer-feature-hello.tgz" {
		t.Errorf("tarball id = %q", tarball.ID)
	}
	if got := tarballFeatureID("https://x/y/devcontainer-feature-hello.tgz?a=b"); got != "hello" {
		t.Errorf("tarballFeatureID = %q", got)
	}
}

// A Feature's updateContentCommand reaches the image metadata like its other
// lifecycle hooks (TS pickFeatureLifecycleHookProperties), so up runs it and
// read-configuration merges it.
func TestFeatureMetadataEntryCarriesEveryLifecycleHook(t *testing.T) {
	set := &features.Set{
		SourceInfo: &features.LocalSource{LocalPath: "./a", UserID: "./a"},
		Features: []features.Feature{{
			ID:                   "a",
			OnCreateCommand:      "create",
			UpdateContentCommand: []any{"one", "two"},
			PostCreateCommand:    "post-create",
			PostStartCommand:     "post-start",
			PostAttachCommand:    "post-attach",
		}},
	}
	entry := featureMetadataEntry(set, false)
	if got, ok := entry.UpdateContentCommand.([]any); !ok || len(got) != 2 || got[0] != "one" {
		t.Errorf("updateContentCommand = %#v, want [one two]", entry.UpdateContentCommand)
	}
	if entry.OnCreateCommand != "create" || entry.PostCreateCommand != "post-create" ||
		entry.PostStartCommand != "post-start" || entry.PostAttachCommand != "post-attach" {
		t.Errorf("hooks = %+v", entry)
	}
	merged := imagemeta.MergeConfiguration([]imagemeta.Entry{entry})
	if len(merged.UpdateContentCommands) != 1 {
		t.Errorf("merged updateContentCommands = %#v, want the Feature's command", merged.UpdateContentCommands)
	}
}
