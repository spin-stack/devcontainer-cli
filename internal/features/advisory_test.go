package features

import (
	"testing"
)

func TestCheckAdvisories_Match(t *testing.T) {
	manifest := &ControlManifest{
		FeatureAdvisories: []FeatureAdvisory{
			{
				FeatureID:           "ghcr.io/devcontainers/features/go",
				IntroducedInVersion: "1.0.0",
				FixedInVersion:      "1.2.0",
				Description:         "security issue",
			},
		},
	}

	featureSets := []*FeatureSet{
		{
			SourceInfo: &OCISource{
				Registry:  "ghcr.io",
				Namespace: "devcontainers/features",
				ID:        "go",
			},
			Features: []Feature{{Version: "1.1.0"}},
		},
	}

	results := CheckAdvisories(manifest, featureSets)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FeatureID != "ghcr.io/devcontainers/features/go" {
		t.Errorf("featureID = %q", results[0].FeatureID)
	}
	if len(results[0].Advisories) != 1 {
		t.Errorf("advisories = %d", len(results[0].Advisories))
	}
}

func TestCheckAdvisories_NoMatch_VersionFixed(t *testing.T) {
	manifest := &ControlManifest{
		FeatureAdvisories: []FeatureAdvisory{
			{
				FeatureID:           "ghcr.io/devcontainers/features/go",
				IntroducedInVersion: "1.0.0",
				FixedInVersion:      "1.2.0",
				Description:         "fixed issue",
			},
		},
	}

	featureSets := []*FeatureSet{
		{
			SourceInfo: &OCISource{
				Registry:  "ghcr.io",
				Namespace: "devcontainers/features",
				ID:        "go",
			},
			Features: []Feature{{Version: "1.3.0"}}, // After fix
		},
	}

	results := CheckAdvisories(manifest, featureSets)
	if len(results) != 0 {
		t.Errorf("expected 0 results for fixed version, got %d", len(results))
	}
}

func TestCheckAdvisories_NoMatch_VersionBefore(t *testing.T) {
	manifest := &ControlManifest{
		FeatureAdvisories: []FeatureAdvisory{
			{
				FeatureID:           "ghcr.io/devcontainers/features/go",
				IntroducedInVersion: "1.5.0",
				FixedInVersion:      "1.6.0",
				Description:         "issue",
			},
		},
	}

	featureSets := []*FeatureSet{
		{
			SourceInfo: &OCISource{
				Registry:  "ghcr.io",
				Namespace: "devcontainers/features",
				ID:        "go",
			},
			Features: []Feature{{Version: "1.4.0"}}, // Before introduction
		},
	}

	results := CheckAdvisories(manifest, featureSets)
	if len(results) != 0 {
		t.Errorf("expected 0 results for version before introduction, got %d", len(results))
	}
}

func TestEnsureNoDisallowedFeatures_Allowed(t *testing.T) {
	manifest := &ControlManifest{
		DisallowedFeatures: []DisallowedFeature{
			{FeatureIDPrefix: "ghcr.io/bad/"},
		},
	}

	features := map[string]interface{}{
		"ghcr.io/good/features/go:1": map[string]interface{}{},
	}

	err := EnsureNoDisallowedFeatures(manifest, features, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureNoDisallowedFeatures_Blocked(t *testing.T) {
	manifest := &ControlManifest{
		DisallowedFeatures: []DisallowedFeature{
			{FeatureIDPrefix: "ghcr.io/bad/", DocumentationURL: "https://example.com"},
		},
	}

	features := map[string]interface{}{
		"ghcr.io/bad/features/evil:1": true,
	}

	err := EnsureNoDisallowedFeatures(manifest, features, nil)
	if err == nil {
		t.Error("expected error for disallowed feature")
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"1.0", []int{1, 0}},
		{"3", []int{3}},
		{"abc", nil},
	}
	for _, tt := range tests {
		got := parseVersion(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("parseVersion(%q) = %v, want nil", tt.input, got)
		}
		if tt.want != nil {
			if len(got) != len(tt.want) {
				t.Errorf("parseVersion(%q) = %v, want %v", tt.input, got, tt.want)
				continue
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseVersion(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
				}
			}
		}
	}
}

func TestIsEarlierVersion(t *testing.T) {
	tests := []struct {
		a, b []int
		want bool
	}{
		{[]int{1, 0, 0}, []int{1, 2, 0}, true},
		{[]int{1, 2, 0}, []int{1, 0, 0}, false},
		{[]int{1, 2, 0}, []int{1, 2, 0}, false},
		{[]int{1}, []int{1, 0}, true},
		{[]int{2, 0}, []int{1, 9, 9}, false},
	}
	for _, tt := range tests {
		got := isEarlierVersion(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("isEarlierVersion(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
