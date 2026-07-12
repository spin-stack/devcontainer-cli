package features

import (
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

func TestStripVersionFromFeatureID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ghcr.io/devcontainers/features/go:1", "ghcr.io/devcontainers/features/go"},
		{"ghcr.io/devcontainers/features/go:1.2.3", "ghcr.io/devcontainers/features/go"},
		{"ghcr.io/devcontainers/features/go@sha256:abc", "ghcr.io/devcontainers/features/go"},
		{"ghcr.io/devcontainers/features/go", "ghcr.io/devcontainers/features/go"},
		{"./local-feature", "./local-feature"},
		{"node:18", "node"},
	}
	for _, tt := range tests {
		got := StripVersionFromID(tt.input)
		if got != tt.want {
			t.Errorf("StripVersionFromID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUserFeaturesToArray(t *testing.T) {
	features := map[string]interface{}{
		"ghcr.io/devcontainers/features/go:1":   map[string]interface{}{"version": "1.21"},
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
