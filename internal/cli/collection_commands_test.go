package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFeature writes a devcontainer-feature.json into <dir>/<name>/.
func writeFeature(t *testing.T, base, name, json string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateDocs_FeatureReadme(t *testing.T) {
	base := t.TempDir()
	// Options are deliberately NOT alphabetical: zeta before alpha. TS preserves the
	// JSON insertion order, so the rows must come out zeta then alpha (a sorted map
	// would flip them).
	writeFeature(t, base, "mytool", `{
		"id": "mytool",
		"version": "1.2.3",
		"name": "My Tool",
		"options": {
			"zeta": { "type": "string", "default": "z", "description": "Zeta opt." },
			"alpha": { "type": "boolean", "default": true, "description": "Alpha opt." },
			"noDefault": { "type": "string" }
		}
	}`)

	if err := generateDocs(base, "ghcr.io", "owner/repo", "owner", "repo", "feature", "info"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(base, "mytool", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(got)

	// Heading is "name (id)".
	if !strings.Contains(readme, "# My Tool (mytool)") {
		t.Errorf("missing/incorrect heading:\n%s", readme)
	}
	// Usage snippet uses the major version.
	if !strings.Contains(readme, `"ghcr.io/owner/repo/mytool:1": {}`) {
		t.Error("usage snippet should pin the major version :1")
	}
	// Options table header matches TS columns.
	if !strings.Contains(readme, "| Options Id | Description | Type | Default Value |") {
		t.Error("options table header does not match TS columns")
	}
	// Insertion order preserved: zeta row before alpha row.
	zeta := strings.Index(readme, "| zeta |")
	alpha := strings.Index(readme, "| alpha |")
	if zeta < 0 || alpha < 0 || zeta > alpha {
		t.Errorf("option order not preserved (zeta=%d alpha=%d)", zeta, alpha)
	}
	// A boolean default renders as "true"; an absent default renders "undefined".
	if !strings.Contains(readme, "| alpha | Alpha opt. | boolean | true |") {
		t.Error("boolean default row incorrect")
	}
	if !strings.Contains(readme, "| noDefault | - | string | undefined |") {
		t.Error("absent-default row should render type '-'/'undefined' like TS")
	}
	// Footer references the config with the github blob URL.
	if !strings.Contains(readme, "https://github.com/owner/repo/blob/main/") {
		t.Error("footer github URL missing")
	}
}

func TestGenerateDocs_SkipsMissingMetadata(t *testing.T) {
	base := t.TempDir()
	// A directory without a devcontainer-feature.json must be skipped, not error.
	if err := os.MkdirAll(filepath.Join(base, "not-a-feature"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := generateDocs(base, "ghcr.io", "o/r", "", "", "feature", "info"); err != nil {
		t.Fatalf("should skip, not error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "not-a-feature", "README.md")); !os.IsNotExist(err) {
		t.Error("no README should be generated for a folder without metadata")
	}
}
