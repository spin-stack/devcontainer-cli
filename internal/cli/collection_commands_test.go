package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDocsTemplate(t *testing.T) {
	got := renderDocsTemplate("#{Name} / #{Name} / #{Unknown}", map[string]string{
		"Name": "tool",
	})
	want := "tool / tool / #{Unknown}"
	if got != want {
		t.Errorf("renderDocsTemplate() = %q, want %q", got, want)
	}
}

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

func TestGenerateDocs_SingleFeatureFolder(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "devcontainer-feature.json"),
		[]byte(`{"id":"solo","version":"1.0.0","name":"Solo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if _, err := os.Stat(filepath.Join(base, "install.sh", "README.md")); err == nil {
		t.Error("#876 a non-directory sibling was treated as a feature folder")
	}
}

func TestGenerateDocs_IgnoresTopLevelFiles(t *testing.T) {
	base := t.TempDir()
	writeFeature(t, base, "realfeat", `{"id":"realfeat","version":"1.0.0","name":"Real"}`)
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("top-level readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := generateDocs(base, "ghcr.io", "o/r", "", "", "feature", "info"); err != nil {
		t.Fatalf("stray top-level file must not error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "realfeat", "README.md")); err != nil {
		t.Errorf("real feature README missing: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "README.md")); string(b) != "top-level readme\n" {
		t.Errorf("#876 top-level README.md was clobbered: %q", b)
	}
}

func TestGenerateDocs_SkipsMissingMetadata(t *testing.T) {
	base := t.TempDir()
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

func TestGenerateDocs_SkipsMetadataThatIsNotValidJSONC(t *testing.T) {
	base := t.TempDir()
	writeFeature(t, base, "broken", `{"id":"broken" "name":"Broken"}`)
	writeFeature(t, base, "jsonc", "// comment\n{\"id\":\"jsonc\",\"name\":\"JSONC\",}")
	if err := generateDocs(base, "ghcr.io", "o/r", "", "", "feature", "info"); err != nil {
		t.Fatalf("an unparsable Feature must be skipped, not fail the command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "broken", "README.md")); !os.IsNotExist(err) {
		t.Errorf("README generated from metadata that is not valid JSONC (stat err %v)", err)
	}
	got, err := os.ReadFile(filepath.Join(base, "jsonc", "README.md"))
	if err != nil {
		t.Fatalf("README for valid JSONC metadata: %v", err)
	}
	if !strings.Contains(string(got), "# JSONC (jsonc)") {
		t.Errorf("README heading missing:\n%s", got)
	}
}
