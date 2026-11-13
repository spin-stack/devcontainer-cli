package features

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyFeatureID(t *testing.T) {
	tests := []struct {
		id   string
		want SourceType
	}{
		{"ghcr.io/devcontainers/features/go:1", SourceOCI},
		{"ghcr.io/devcontainers/features/node:latest", SourceOCI},
		{"myregistry.azurecr.io/myns/myfeature:1.0", SourceOCI},
		{"localhost:5000/ns/feat:1", SourceOCI},
		{"owner/repo/feat", SourceOCI},
		{"owner/feat", SourceOCI},
		{`a\b`, SourceOCI},
		{"https://example.com/feature.tgz", SourceDirectTarball},
		{"http://example.com/feature.tgz", SourceDirectTarball},
		{"./local-feature", SourceLocalPath},
		{"../sibling-feature", SourceLocalPath},
		{"/abs/feature", SourceLocalPath},
		{"go", SourceLegacyShorthand},
		{"node", SourceLegacyShorthand},
		{"node:18", SourceLegacyShorthand},
		{"my-feature", SourceLegacyShorthand},
	}
	for _, tt := range tests {
		got := ClassifyID(tt.id)
		if got != tt.want {
			t.Errorf("ClassifyID(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestBackwardCompatibleID(t *testing.T) {
	deprecated := func(id, src string) string {
		return "(!) WARNING: Using the deprecated '" + id + "' Feature. See https://github.com/devcontainers/features/tree/main/src/" + src + "#example-usage for the updated Feature."
	}
	tests := []struct {
		id, wantID, wantWarning string
	}{
		{"git", "ghcr.io/devcontainers/features/git:1", deprecated("git", "git")},
		{"node", "ghcr.io/devcontainers/features/node:1", deprecated("node", "node")},
		{"golang", "ghcr.io/devcontainers/features/go:1", deprecated("golang", "go")},
		{"common", "ghcr.io/devcontainers/features/common-utils:1", deprecated("common", "common-utils")},
		{"gradle", "gradle", "(!) WARNING: Falling back to the deprecated 'gradle' Feature. It is now part of the 'java' Feature. See https://github.com/devcontainers/features/tree/main/src/java#options for the updated Feature."},
		{"node:18", "node:18", ""},
		{"node:1", "node:1", ""},
		{"golang:1", "golang:1", ""},
		{"my-custom-feature", "my-custom-feature", ""},
		{"owner/repo/feat", "owner/repo/feat", ""},
		{"ghcr.io/devcontainers/features/go:1", "ghcr.io/devcontainers/features/go:1", ""},
		{"./local-feature", "./local-feature", ""},
	}
	for _, tt := range tests {
		id, warning := BackwardCompatibleID(tt.id)
		if id != tt.wantID || warning != tt.wantWarning {
			t.Errorf("BackwardCompatibleID(%q) = %q, %q; want %q, %q", tt.id, id, warning, tt.wantID, tt.wantWarning)
		}
	}
}

func TestUpdateDeprecatedFeaturesIntoOptions(t *testing.T) {
	in := []DevContainerFeature{
		{UserFeatureID: "ghcr.io/devcontainers/features/java:1", Options: "17"},
		{UserFeatureID: "gradle", Options: map[string]any{"ignored": true}},
		{UserFeatureID: "jupyterlab", Options: true},
		{UserFeatureID: "node:18", Options: map[string]any{}},
	}
	got, warnings := UpdateDeprecatedFeaturesIntoOptions(in)
	want := []DevContainerFeature{
		{UserFeatureID: "ghcr.io/devcontainers/features/java:1", Options: map[string]any{"version": "17", "installGradle": true}},
		{UserFeatureID: "node:18", Options: map[string]any{}},
		{UserFeatureID: "ghcr.io/devcontainers/features/python:1", Options: map[string]any{"installJupyterlab": true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("features = %v, want %v", got, want)
	}
	if len(warnings) != 2 || !strings.HasPrefix(warnings[0], "(!) WARNING: Using the deprecated 'gradle' Feature. It is now part of the 'java' Feature.") {
		t.Errorf("warnings = %q", warnings)
	}
	if in[0].Options != "17" || len(in) != 4 {
		t.Errorf("the input was modified: %v", in)
	}
}

func TestUserFeaturesToArray(t *testing.T) {
	features := map[string]any{
		"ghcr.io/devcontainers/features/go:1":   map[string]any{"version": "1.21"},
		"ghcr.io/devcontainers/features/node:1": true,
	}
	arr := UserFeaturesToArray(features)
	if len(arr) != 2 {
		t.Errorf("len = %d, want 2", len(arr))
	}
}

func TestUserFeaturesToArray_Nil(t *testing.T) {
	arr := UserFeaturesToArray(nil)
	if arr != nil {
		t.Errorf("expected nil, got %v", arr)
	}
}

func TestLocalFeaturePath(t *testing.T) {
	ws := t.TempDir()
	dc := filepath.Join(ws, ".devcontainer")
	outside := "Local file path parse error. Resolved path must be a child of the .devcontainer/ folder.  Parsed: "
	for _, tc := range []struct {
		name, id, configDir, workspaceRoot string
		want, wantErr                      string
	}{
		{name: "inside .devcontainer", id: "./feat", configDir: dc, workspaceRoot: ws, want: filepath.Join(dc, "feat")},
		{name: "nested", id: "./a/../b/feat", configDir: dc, workspaceRoot: ws, want: filepath.Join(dc, "b", "feat")},
		{name: "the .devcontainer folder itself", id: "./", configDir: dc, workspaceRoot: ws, want: dc},
		{name: "parent folder", id: "../feat", configDir: dc, workspaceRoot: ws, wantErr: outside + filepath.Join(ws, "feat")},
		{name: "configuration at the workspace root", id: "./feat", configDir: ws, workspaceRoot: ws, wantErr: outside + filepath.Join(ws, "feat")},
		{name: "configuration of another workspace", id: "./feat", configDir: dc, workspaceRoot: filepath.Join(ws, "other"), wantErr: outside + filepath.Join(dc, "feat")},
		{name: "dots in a folder name", id: "./a..b", configDir: dc, workspaceRoot: ws, wantErr: outside + filepath.Join(dc, "a..b")},
		{name: "absolute", id: filepath.Join(dc, "feat"), configDir: dc, workspaceRoot: ws, wantErr: "An Absolute path to a local feature is not allowed."},
		{name: "absolute without a configuration", id: "/feat", wantErr: "An Absolute path to a local feature is not allowed."},
		{name: "no configuration", id: "./feat", workspaceRoot: ws, wantErr: "A local feature requires a configuration path."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LocalFeaturePath(tc.id, tc.configDir, tc.workspaceRoot)
			if tc.wantErr != "" {
				invalid, ok := errors.AsType[*InvalidLocalFeatureError](err)
				if !ok || invalid.Error() != tc.wantErr || got != "" {
					t.Fatalf("LocalFeaturePath = %q, %v; want the error %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("LocalFeaturePath = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLocalFeaturePathDefaultsToTheWorkingDirectory(t *testing.T) {
	ws := t.TempDir()
	t.Chdir(ws)
	dc := filepath.Join(ws, ".devcontainer")
	if got, err := LocalFeaturePath("./feat", dc, ""); err != nil || got != filepath.Join(dc, "feat") {
		t.Fatalf("LocalFeaturePath = %q, %v; want the Feature in the working directory's .devcontainer", got, err)
	}
	if _, err := LocalFeaturePath("./feat", filepath.Join(ws, "sub", ".devcontainer"), ""); err == nil {
		t.Fatal("a configuration outside the working directory's .devcontainer resolved")
	}
	if got, err := LocalFeaturePath("./feat", dc, "."); err != nil || got != filepath.Join(dc, "feat") {
		t.Fatalf("relative workspace root: LocalFeaturePath = %q, %v", got, err)
	}
}
