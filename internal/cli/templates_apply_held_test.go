package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestTemplateSelection(t *testing.T) {
	sel, err := templateSelection("ghcr.io/o/t/node:1", `{"version": "20"}`, `[{"id":"ghcr.io/o/f/go:1"}]`, `[".github/*"]`)
	if err != nil {
		t.Fatal(err)
	}
	if sel.ID != "ghcr.io/o/t/node:1" || sel.Options["version"] != "20" {
		t.Errorf("selection = %+v", sel)
	}
	if len(sel.Features) != 1 || sel.Features[0].ID != "ghcr.io/o/f/go:1" {
		t.Errorf("features = %+v", sel.Features)
	}
	if !slices.Equal(sel.OmitPaths, []string{".github/*"}) {
		t.Errorf("omit paths = %q", sel.OmitPaths)
	}

	// Empty flags select nothing extra; a malformed --omit-paths omits nothing.
	bare, err := templateSelection("ghcr.io/o/t/node:1", "", "", "[")
	if err != nil {
		t.Fatal(err)
	}
	if len(bare.Options) != 0 || len(bare.Features) != 0 || len(bare.OmitPaths) != 0 {
		t.Errorf("bare selection = %+v, want nothing besides the id", bare)
	}
}

func TestTemplateSelectionRejects(t *testing.T) {
	cases := []struct {
		name, id, args, features, want string
	}{
		{"missing id", "", "{}", "[]", "Missing required argument: --template-id"},
		{"args not an object", "x", `["a"]`, "[]", "Invalid template arguments provided."},
		{"malformed args", "x", `{`, "[]", "Invalid template arguments provided."},
		{"malformed features", "x", "{}", `[{`, "Invalid features JSON"},
	}
	for _, c := range cases {
		_, err := templateSelection(c.id, c.args, c.features, "[]")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// A template that cannot be fetched fails the command without printing files.
func TestTemplatesApplyReportsFetchError(t *testing.T) {
	cmd := realTemplatesApplyCmd()
	out := &captureOutput{}
	cmd.SetOut(&out.out)
	cmd.SetErr(&out.err)
	cmd.SetArgs([]string{"--template-id", "UPPER/Not A Ref", "--workspace-folder", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "parse template ID") {
		t.Fatalf("err = %v, want the template id parse error", err)
	}
	if strings.Contains(out.out.String(), `"files"`) {
		t.Errorf("stdout = %q, want no files result", out.out.String())
	}
}
