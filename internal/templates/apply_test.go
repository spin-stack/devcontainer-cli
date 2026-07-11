package templates

import (
	"testing"
)

func TestSubstituteTemplateOptions(t *testing.T) {
	tests := []struct {
		content string
		options map[string]string
		want    string
	}{
		{
			content: `{"image": "${templateOption:baseImage}"}`,
			options: map[string]string{"baseImage": "ubuntu:22.04"},
			want:    `{"image": "ubuntu:22.04"}`,
		},
		{
			content: "FROM ${templateOption:baseImage}\nRUN echo ${templateOption:greeting}",
			options: map[string]string{"baseImage": "alpine", "greeting": "hello"},
			want:    "FROM alpine\nRUN echo hello",
		},
		{
			content: "no options here",
			options: map[string]string{},
			want:    "no options here",
		},
		{
			content: "${templateOption:missing}",
			options: map[string]string{},
			want:    "${templateOption:missing}", // unresolved passthrough
		},
		{
			content: "${templateOption: spacedName }",
			options: map[string]string{"spacedName": "value"},
			want:    "value",
		},
	}

	for _, tt := range tests {
		got := substituteTemplateOptions(tt.content, tt.options)
		if got != tt.want {
			t.Errorf("substituteTemplateOptions(%q, %v) = %q, want %q", tt.content, tt.options, got, tt.want)
		}
	}
}

func TestSelectedTemplate_OmitPaths(t *testing.T) {
	// Verify omit path parsing
	selected := SelectedTemplate{
		ID:        "ghcr.io/devcontainers/templates/test:1",
		OmitPaths: []string{".github/*", "README.md"},
	}

	// Dirs
	dirOmits := 0
	fileOmits := 0
	for _, p := range selected.OmitPaths {
		if len(p) > 2 && p[len(p)-2:] == "/*" {
			dirOmits++
		} else {
			fileOmits++
		}
	}
	if dirOmits != 1 {
		t.Errorf("dir omits = %d, want 1", dirOmits)
	}
	if fileOmits != 1 {
		t.Errorf("file omits = %d, want 1", fileOmits)
	}
}
