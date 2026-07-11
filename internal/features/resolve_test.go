package features

import (
	"testing"
)

func TestClassifyFeatureID(t *testing.T) {
	tests := []struct {
		id   string
		want FeatureSourceType
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
		got := ClassifyFeatureID(tt.id)
		if got != tt.want {
			t.Errorf("ClassifyFeatureID(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestResolveFeatureID_OCI(t *testing.T) {
	id, mapped := ResolveFeatureID("ghcr.io/devcontainers/features/go:1", false)
	if id != "ghcr.io/devcontainers/features/go:1" {
		t.Errorf("id = %q", id)
	}
	if mapped {
		t.Error("OCI features should not be mapped")
	}
}

func TestResolveFeatureID_LegacyMigratedPinsV1(t *testing.T) {
	// A migrated legacy shorthand pins :1 (versionBackwardComp).
	id, mapped := ResolveFeatureID("git", false)
	if id != "ghcr.io/devcontainers/features/git:1" {
		t.Errorf("id = %q", id)
	}
	if !mapped {
		t.Error("should be mapped")
	}
}

func TestResolveFeatureID_LegacyRenamed(t *testing.T) {
	// golang → go, common → common-utils, pinned :1.
	if id, _ := ResolveFeatureID("golang", false); id != "ghcr.io/devcontainers/features/go:1" {
		t.Errorf("golang = %q", id)
	}
	if id, _ := ResolveFeatureID("common", false); id != "ghcr.io/devcontainers/features/common-utils:1" {
		t.Errorf("common = %q", id)
	}
}

func TestResolveFeatureID_LegacyWithVersion(t *testing.T) {
	// An explicit version tag is honored instead of the :1 pin.
	id, mapped := ResolveFeatureID("node:18", false)
	if id != "ghcr.io/devcontainers/features/node:18" {
		t.Errorf("id = %q", id)
	}
	if !mapped {
		t.Error("should be mapped")
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

func TestResolveFeatureID_SkipAutoMapping(t *testing.T) {
	id, mapped := ResolveFeatureID("go", true)
	if id != "go" {
		t.Errorf("id = %q (should not be mapped)", id)
	}
	if mapped {
		t.Error("should not be mapped when skipAutoMapping is true")
	}
}

func TestResolveFeatureID_LegacyUnknown(t *testing.T) {
	id, mapped := ResolveFeatureID("my-custom-feature", false)
	if id != "ghcr.io/devcontainers/features/my-custom-feature:1" {
		t.Errorf("id = %q", id)
	}
	if !mapped {
		t.Error("unknown shorthand should be auto-mapped to ghcr.io/devcontainers/features/")
	}
}

func TestResolveFeatureID_LocalPath(t *testing.T) {
	id, mapped := ResolveFeatureID("./local-feature", false)
	if id != "./local-feature" {
		t.Errorf("id = %q", id)
	}
	if mapped {
		t.Error("local paths should not be mapped")
	}
}

func TestResolveFeatureID_Tarball(t *testing.T) {
	id, mapped := ResolveFeatureID("https://example.com/feature.tgz", false)
	if id != "https://example.com/feature.tgz" {
		t.Errorf("id = %q", id)
	}
	if mapped {
		t.Error("tarballs should not be mapped")
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
		got := StripVersionFromFeatureID(tt.input)
		if got != tt.want {
			t.Errorf("StripVersionFromFeatureID(%q) = %q, want %q", tt.input, got, tt.want)
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
