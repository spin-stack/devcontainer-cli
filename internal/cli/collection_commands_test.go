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

	// The insertion-order guarantee is a positional check, kept separate: the zeta
	// row must precede the alpha row (a sorted map would flip them).
	zeta := strings.Index(readme, "| zeta |")
	alpha := strings.Index(readme, "| alpha |")
	if zeta < 0 || alpha < 0 || zeta > alpha {
		t.Errorf("option insertion order not preserved (zeta=%d alpha=%d)", zeta, alpha)
	}

	wants := []struct{ desc, sub string }{
		{"heading is 'name (id)'", "# My Tool (mytool)"},
		{"usage snippet pins the major version", `"ghcr.io/owner/repo/mytool:1": {}`},
		{"options table uses the TS columns", "| Options Id | Description | Type | Default Value |"},
		{"boolean default renders 'true'", "| alpha | Alpha opt. | boolean | true |"},
		{"absent default renders '-'/'undefined'", "| noDefault | - | string | undefined |"},
		{"footer has the github blob URL", "https://github.com/owner/repo/blob/main/"},
	}
	for _, w := range wants {
		if !strings.Contains(readme, w.sub) {
			t.Errorf("%s: missing %q\n--- README ---\n%s", w.desc, w.sub, readme)
		}
	}
}

// TestGenerateDocs_SingleFeatureFolder covers upstream #876: pointing at a
// folder that directly holds devcontainer-feature.json documents THAT folder,
// instead of iterating its own files as if each were a separate feature.
func TestGenerateDocs_SingleFeatureFolder(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "devcontainer-feature.json"),
		[]byte(`{"id":"solo","version":"1.0.0","name":"Solo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling file that used to be (wrongly) treated as a sub-feature directory.
	if err := os.WriteFile(filepath.Join(base, "install.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := generateDocs(base, "ghcr.io", "o/r", "", "", "feature", "info"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(base, "README.md"))
	if err != nil {
		t.Fatalf("#876 single-folder README not generated: %v", err)
	}
	if !strings.Contains(string(got), "# Solo (solo)") {
		t.Errorf("#876 README heading missing:\n%s", got)
	}
	// The sibling file must not have been treated as a feature directory.
	if _, err := os.Stat(filepath.Join(base, "install.sh", "README.md")); err == nil {
		t.Error("#876 a non-directory sibling was treated as a feature folder")
	}
}

// TestGenerateDocs_IgnoresTopLevelFiles covers upstream #876: top-level files
// (not directories) must never be treated as collection members.
func TestGenerateDocs_IgnoresTopLevelFiles(t *testing.T) {
	base := t.TempDir()
	writeFeature(t, base, "realfeat", `{"id":"realfeat","version":"1.0.0","name":"Real"}`)
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("top-level readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := generateDocs(base, "ghcr.io", "o/r", "", "", "feature", "info"); err != nil {
		t.Fatalf("stray top-level file must not error: %v", err)
	}
	// The real feature still got its README...
	if _, err := os.Stat(filepath.Join(base, "realfeat", "README.md")); err != nil {
		t.Errorf("real feature README missing: %v", err)
	}
	// ...and the top-level README.md was left untouched (not overwritten from a template).
	if b, _ := os.ReadFile(filepath.Join(base, "README.md")); string(b) != "top-level readme\n" {
		t.Errorf("#876 top-level README.md was clobbered: %q", b)
	}
}

// TestWriteCollectionMetadata_WriteError locks the fix that a failed write of
// devcontainer-collection.json surfaces as an error instead of a false success.
func TestWriteCollectionMetadata_WriteError(t *testing.T) {
	// Writing into a directory that does not exist fails; the error must propagate.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := writeCollectionMetadata(missing, "feature", nil); err == nil {
		t.Fatal("expected an error writing into a missing directory, got nil")
	}
	// The happy path still writes the file.
	ok := t.TempDir()
	if err := writeCollectionMetadata(ok, "feature", nil); err != nil {
		t.Fatalf("unexpected error on the happy path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ok, "devcontainer-collection.json")); err != nil {
		t.Errorf("collection metadata not written: %v", err)
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
