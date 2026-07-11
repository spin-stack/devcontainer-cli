package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/pfs"
)

func TestEscapeJSONPointer(t *testing.T) {
	cases := map[string]string{
		"simple":                         "simple",
		"ghcr.io/devcontainers/features": "ghcr.io~1devcontainers~1features",
		"a~b":                            "a~0b",
		"a/b~c":                          "a~1b~0c",
	}
	for in, want := range cases {
		if got := escapeJSONPointer(in); got != want {
			t.Errorf("escapeJSONPointer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstituteTemplateOptions_Pure(t *testing.T) {
	opts := map[string]string{"imageVariant": "bookworm", "nodeVersion": "20"}
	in := `FROM base:${templateOption:imageVariant}\nNODE=${templateOption:nodeVersion} keep=${templateOption:missing}`
	want := `FROM base:bookworm\nNODE=20 keep=${templateOption:missing}`
	if got := substituteTemplateOptions(in, opts); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// whitespace inside the braces is tolerated.
	if got := substituteTemplateOptions("${templateOption: imageVariant }", opts); got != "bookworm" {
		t.Errorf("whitespace variant = %q", got)
	}
}

func TestApplyOptionDefaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "devcontainer-template.json"), []byte(`{
		"id": "x",
		"options": {
			"imageVariant": { "type": "string", "default": "bookworm" },
			"installZsh":   { "type": "boolean", "default": true },
			"noDefault":    { "type": "string" }
		}
	}`), 0644)

	// User provided imageVariant → kept; others filled from defaults (or skipped).
	got := applyOptionDefaults(pfs.OSFS{}, dir, map[string]string{"imageVariant": "bullseye"}, log.Null)
	if got["imageVariant"] != "bullseye" {
		t.Errorf("imageVariant = %q (user value should win)", got["imageVariant"])
	}
	if got["installZsh"] != "true" {
		t.Errorf("installZsh = %q, want boolean default true", got["installZsh"])
	}
	if _, ok := got["noDefault"]; ok {
		t.Errorf("noDefault should have no value (no default): %q", got["noDefault"])
	}
}

func TestApplyOptionDefaults_NoMetadata(t *testing.T) {
	// Missing devcontainer-template.json → returns the user options unchanged.
	dir := t.TempDir()
	got := applyOptionDefaults(pfs.OSFS{}, dir, map[string]string{"a": "b"}, log.Null)
	if len(got) != 1 || got["a"] != "b" {
		t.Errorf("got %v", got)
	}
}
