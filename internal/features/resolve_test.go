package features

import (
	"errors"
	"path/filepath"
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
		{"https://example.com/feature.tgz", SourceDirectTarball},
		{"http://example.com/feature.tgz", SourceDirectTarball},
		{"./local-feature", SourceLocalPath},
		{"../sibling-feature", SourceLocalPath},
		{"/abs/feature", SourceLocalPath},
		{"go", SourceLegacyShorthand},
		{"node", SourceLegacyShorthand},
		{"my-feature", SourceLegacyShorthand},
	}
	for _, tt := range tests {
		got := ClassifyID(tt.id)
		if got != tt.want {
			t.Errorf("ClassifyID(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestResolveFeatureID(t *testing.T) {
	tests := []struct {
		name            string
		id              string
		skipAutoMapping bool
		wantID          string
		wantMapped      bool
	}{
		{
			name:       "OCI",
			id:         "ghcr.io/devcontainers/features/go:1",
			wantID:     "ghcr.io/devcontainers/features/go:1",
			wantMapped: false,
		},
		{
			// A migrated legacy shorthand pins :1 (versionBackwardComp).
			name:       "LegacyMigratedPinsV1",
			id:         "git",
			wantID:     "ghcr.io/devcontainers/features/git:1",
			wantMapped: true,
		},
		{
			// golang → go, pinned :1.
			name:       "LegacyRenamed_golang",
			id:         "golang",
			wantID:     "ghcr.io/devcontainers/features/go:1",
			wantMapped: true,
		},
		{
			// common → common-utils, pinned :1.
			name:       "LegacyRenamed_common",
			id:         "common",
			wantID:     "ghcr.io/devcontainers/features/common-utils:1",
			wantMapped: true,
		},
		{
			// An explicit version tag is honored instead of the :1 pin.
			name:       "LegacyWithVersion",
			id:         "node:18",
			wantID:     "ghcr.io/devcontainers/features/node:18",
			wantMapped: true,
		},
		{
			name:            "SkipAutoMapping",
			id:              "go",
			skipAutoMapping: true,
			wantID:          "go",
			wantMapped:      false,
		},
		{
			// unknown shorthand should be auto-mapped to ghcr.io/devcontainers/features/.
			name:       "LegacyUnknown",
			id:         "my-custom-feature",
			wantID:     "ghcr.io/devcontainers/features/my-custom-feature:1",
			wantMapped: true,
		},
		{
			name:       "LocalPath",
			id:         "./local-feature",
			wantID:     "./local-feature",
			wantMapped: false,
		},
		{
			name:       "Tarball",
			id:         "https://example.com/feature.tgz",
			wantID:     "https://example.com/feature.tgz",
			wantMapped: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, mapped := ResolveID(tt.id, tt.skipAutoMapping)
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
			if mapped != tt.wantMapped {
				t.Errorf("mapped = %v, want %v", mapped, tt.wantMapped)
			}
		})
	}
}

func TestDeprecatedFeatureIntoOptions(t *testing.T) {
	// gradle/maven fold into java, jupyterlab into python, each with a flag.
	for name, want := range map[string]struct{ mapTo, opt string }{
		"gradle":     {"java", "installGradle"},
		"maven":      {"java", "installMaven"},
		"jupyterlab": {"python", "installJupyterlab"},
	} {
		m, ok := DeprecatedFeatureIntoOptions[name]
		if !ok || m.MapTo != want.mapTo || m.Option != want.opt {
			t.Errorf("%s → %+v, want %+v", name, m, want)
		}
		if !IsKnownLegacyFeature(name) {
			t.Errorf("%s should be a known legacy feature", name)
		}
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

// A local Feature resolves next to its configuration, and only inside the
// workspace's .devcontainer folder, as TS processFeatureIdentifier
// (containerFeaturesConfiguration.ts) requires; each refusal is the error TS
// logs.
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
		// TS looks for ".." anywhere in the relative path, a folder name included.
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

// Without a workspace folder the workspace root is the working directory, as
// TS's params.cwd falls back to process.cwd().
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
