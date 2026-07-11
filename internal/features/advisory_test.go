package features

import (
	"errors"
	"testing"
)

func TestCheckAdvisories(t *testing.T) {
	// One advisory on the go feature, active for [introduced, fixed). Vary the
	// installed version and assert whether it is flagged.
	tests := []struct {
		name        string
		introduced  string
		fixed       string
		version     string
		wantResults int
	}{
		{"version in affected range", "1.0.0", "1.2.0", "1.1.0", 1},
		{"version at/after the fix", "1.0.0", "1.2.0", "1.3.0", 0},
		{"version before introduction", "1.5.0", "1.6.0", "1.4.0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &ControlManifest{FeatureAdvisories: []FeatureAdvisory{{
				FeatureID:           "ghcr.io/devcontainers/features/go",
				IntroducedInVersion: tt.introduced,
				FixedInVersion:      tt.fixed,
				Description:         "issue",
			}}}
			featureSets := []*FeatureSet{{
				SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "devcontainers/features", ID: "go"},
				Features:   []Feature{{Version: tt.version}},
			}}

			results := CheckAdvisories(manifest, featureSets)
			if len(results) != tt.wantResults {
				t.Fatalf("results = %d, want %d", len(results), tt.wantResults)
			}
			if tt.wantResults > 0 {
				if results[0].FeatureID != "ghcr.io/devcontainers/features/go" || len(results[0].Advisories) != 1 {
					t.Errorf("unexpected result: %+v", results[0])
				}
			}
		})
	}
}

func TestEnsureNoDisallowedFeatures(t *testing.T) {
	const docURL = "https://example.com"
	// A prefix only matches on a separator boundary (/ : @) or exact length, like TS,
	// so it must not block a feature that merely shares the prefix's characters. When
	// blocked, the returned *DisallowedFeatureError carries the id and doc URL.
	tests := []struct {
		name        string
		prefix      string
		docURL      string
		featureID   string
		wantBlocked bool
	}{
		{"unrelated feature allowed", "ghcr.io/bad/", "", "ghcr.io/good/features/go:1", false},
		{"exact-prefix version blocked", "ghcr.io/bad/features/evil", docURL, "ghcr.io/bad/features/evil:1", true},
		{"boundary: exact match", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool", true},
		{"boundary: ':' separator", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool:1", true},
		{"boundary: '@' separator", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool@sha256:x", true},
		{"boundary: shared chars not blocked", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/toolkit:1", false},
		{"boundary: suffix not blocked", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tooling", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &ControlManifest{DisallowedFeatures: []DisallowedFeature{
				{FeatureIDPrefix: tt.prefix, DocumentationURL: tt.docURL},
			}}
			err := EnsureNoDisallowedFeatures(manifest, map[string]interface{}{tt.featureID: true}, nil)

			if (err != nil) != tt.wantBlocked {
				t.Fatalf("blocked=%v, want %v (err=%v)", err != nil, tt.wantBlocked, err)
			}
			if !tt.wantBlocked {
				return
			}
			var dfe *DisallowedFeatureError
			if !errors.As(err, &dfe) {
				t.Fatalf("expected *DisallowedFeatureError, got %T", err)
			}
			if dfe.FeatureID != tt.featureID || dfe.DocumentationURL != tt.docURL {
				t.Errorf("error fields = {%q, %q}, want {%q, %q}", dfe.FeatureID, dfe.DocumentationURL, tt.featureID, tt.docURL)
			}
		})
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
