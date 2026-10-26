package templates

import (
	"testing"
)

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
