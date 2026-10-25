package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDocsHeader(t *testing.T) {
	const note = "### **IMPORTANT NOTE**\n"
	const deprecated = "- **This Feature is deprecated, and will no longer receive any further updates/support.**\n"
	cases := []struct {
		name string
		meta map[string]any
		want string
	}{
		{"current", map[string]any{"id": "x"}, ""},
		{"deprecated", map[string]any{"deprecated": true}, note + deprecated},
		{"one legacy id", map[string]any{"legacyIds": []any{"old"}}, note + "- **Ids used to publish this Feature in the past - 'old'**\n"},
		{"both", map[string]any{"deprecated": true, "legacyIds": []any{"a", "b"}}, note + deprecated + "- **Ids used to publish this Feature in the past - 'a', 'b'**\n"},
	}
	for _, c := range cases {
		if got := generateDocsHeader(c.meta); got != c.want {
			t.Errorf("%s: header = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGenerateCustomizationsMarkdown(t *testing.T) {
	if got := generateCustomizationsMarkdown(map[string]any{}); got != "" {
		t.Errorf("no extensions: %q, want empty", got)
	}
	meta := map[string]any{"customizations": map[string]any{"vscode": map[string]any{"extensions": []any{"golang.go"}}}}
	want := "\n## Customizations\n\n### VS Code Extensions\n\n- `golang.go`\n"
	if got := generateCustomizationsMarkdown(meta); got != want {
		t.Errorf("customizations = %q, want %q", got, want)
	}
}

func TestOptionCells(t *testing.T) {
	if got := jsTruthyString("", "-"); got != "-" {
		t.Errorf("empty string cell = %q, want the fallback", got)
	}
	if got := jsTruthyString(3.0, "-"); got != "-" {
		t.Errorf("non-string cell = %q, want the fallback", got)
	}
	if got := jsTruthyString("text", "-"); got != "text" {
		t.Errorf("string cell = %q", got)
	}
	defaults := []struct {
		ov   map[string]any
		want string
	}{
		{map[string]any{}, "undefined"},
		{map[string]any{"default": ""}, "-"},
		{map[string]any{"default": "x"}, "x"},
		{map[string]any{"default": 1.5}, "1.5"},
		{map[string]any{"default": false}, "false"},
		{map[string]any{"default": nil}, "null"},
	}
	for _, d := range defaults {
		if got := defaultCell(d.ov); got != d.want {
			t.Errorf("defaultCell(%v) = %q, want %q", d.ov, got, d.want)
		}
	}
}

// generate-docs skips dot folders, appends NOTES.md, links the metadata on
// GitHub only when both owner and repo are given, and puts a deprecated
// Feature's note before the README.
func TestGenerateDocsReadmeParts(t *testing.T) {
	base := t.TempDir()
	writeFeature(t, base, ".hidden", `{"id":"hidden","version":"1.0.0"}`)
	writeFeature(t, base, "old", `{"id":"old","version":"2.1.0","deprecated":true}`)
	if err := os.WriteFile(filepath.Join(base, "old", "NOTES.md"), []byte("Read these notes."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generateDocs(base, "ghcr.io", "o/r", "owner", "", "feature", "info"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, ".hidden", "README.md")); !os.IsNotExist(err) {
		t.Errorf("a dot folder was documented (stat err %v)", err)
	}
	b, err := os.ReadFile(filepath.Join(base, "old", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	if !strings.HasPrefix(readme, "### **IMPORTANT NOTE**\n- **This Feature is deprecated") {
		t.Errorf("README does not start with the deprecation note:\n%s", readme)
	}
	if !strings.Contains(readme, "Read these notes.") {
		t.Errorf("NOTES.md missing:\n%s", readme)
	}
	if strings.Contains(readme, "https://github.com/") {
		t.Errorf("GitHub link without a repo:\n%s", readme)
	}
	if !strings.Contains(readme, `"ghcr.io/o/r/old:2": {}`) {
		t.Errorf("usage does not pin the major version:\n%s", readme)
	}
}
