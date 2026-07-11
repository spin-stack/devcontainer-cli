package oci

import (
	"strings"
	"testing"
)

func TestComputeTags(t *testing.T) {
	tests := []struct {
		version string
		want    []string
	}{
		{"1.2.3", []string{"1.2.3", "1.2", "1", "latest"}},
		{"2.0", []string{"2.0", "2", "latest"}},
		{"3", []string{"3", "latest"}},
		{"1.0.0", []string{"1.0.0", "1.0", "1", "latest"}},
		{"1.2.3.4", []string{"1.2.3.4", "1.2", "1", "latest"}}, // unusual four-part version
	}

	for _, tt := range tests {
		got := computeTags(tt.version)
		if len(got) != len(tt.want) {
			t.Errorf("computeTags(%q) = %v, want %v", tt.version, got, tt.want)
			continue
		}
		for i, g := range got {
			if g != tt.want[i] {
				t.Errorf("computeTags(%q)[%d] = %q, want %q", tt.version, i, g, tt.want[i])
			}
		}
	}
}

func TestGetSemanticTags(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		published []string
		wantTags  []string
		wantSkip  bool
		wantErr   bool
	}{
		{
			name:      "first publish (no existing tags)",
			version:   "1.2.3",
			published: nil,
			wantTags:  []string{"1", "1.2", "1.2.3", "latest"},
		},
		{
			name:      "exact version already exists -> skip",
			version:   "1.2.3",
			published: []string{"1", "1.2", "1.2.3", "latest"},
			wantSkip:  true,
		},
		{
			name:      "newer patch advances all floating tags",
			version:   "1.2.4",
			published: []string{"1", "1.2", "1.2.3", "latest"},
			wantTags:  []string{"1", "1.2", "1.2.4", "latest"},
		},
		{
			name:      "older patch does NOT move latest/major/minor back",
			version:   "1.2.1",
			published: []string{"1", "1.2", "1.2.3", "latest"},
			wantTags:  []string{"1.2.1"},
		},
		{
			name:      "new minor advances major+latest but is its own minor",
			version:   "1.3.0",
			published: []string{"1", "1.2", "1.2.5", "latest"},
			wantTags:  []string{"1", "1.3", "1.3.0", "latest"},
		},
		{
			// 1.5.0 is newest in the 1.x line (advances "1" and "1.5") but a 2.0.0
			// exists, so it must NOT move "latest".
			name:      "newer within older major line does not touch latest",
			version:   "1.5.0",
			published: []string{"2", "2.0", "2.0.0", "1", "1.4", "1.4.0", "latest"},
			wantTags:  []string{"1", "1.5", "1.5.0"},
		},
		{
			name:      "invalid semver -> error",
			version:   "1.2.3.4",
			published: nil,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags, skip, err := GetSemanticTags(tt.version, tt.published)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if skip != tt.wantSkip {
				t.Fatalf("skip = %v, want %v", skip, tt.wantSkip)
			}
			if skip {
				return
			}
			if strings.Join(tags, ",") != strings.Join(tt.wantTags, ",") {
				t.Errorf("tags = %v, want %v", tags, tt.wantTags)
			}
		})
	}
}
