package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageSingleFeature(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "devcontainer-feature.json"), `{"id":"solo","version":"1.0.0","name":1}`, 0o644)
	writeFile(t, filepath.Join(src, "install.sh"), "#!/bin/sh\n", 0o755)
	out := filepath.Join(t.TempDir(), "out")
	if err := packageCollection(src, out, "feature", false, "info"); err != nil {
		t.Fatalf("package: %v", err)
	}
	if got := tarFile(t, filepath.Join(out, "devcontainer-feature-solo.tgz"), "./install.sh"); got != "#!/bin/sh\n" {
		t.Errorf("install.sh in archive = %q", got)
	}
	if got := readCollection(t, out); !strings.Contains(got, `"id": "solo"`) {
		t.Errorf("collection:\n%s", got)
	}
}

func TestPackageRequiredProperties(t *testing.T) {
	bad := map[string]string{
		"no id":       `{"version":"1.0.0","name":"N"}`,
		"no version":  `{"id":"x","name":"N"}`,
		"no name":     `{"id":"x","version":"1.0.0"}`,
		"zero name":   `{"id":"x","version":"1.0.0","name":0}`,
		"not parsing": `{"id":"x",`,
	}
	for name, meta := range bad {
		single := t.TempDir()
		writeFile(t, filepath.Join(single, "devcontainer-feature.json"), meta, 0o644)
		writeFile(t, filepath.Join(single, "install.sh"), "#!/bin/sh\n", 0o755)
		if err := packageCollection(single, filepath.Join(t.TempDir(), "out"), "feature", false, "info"); exitCode(err) != 1 {
			t.Errorf("single, %s: err = %v, want exit 1", name, err)
		}

		coll := t.TempDir()
		writeFile(t, filepath.Join(coll, "x", "devcontainer-feature.json"), meta, 0o644)
		writeFile(t, filepath.Join(coll, "x", "install.sh"), "#!/bin/sh\n", 0o755)
		if err := packageCollection(coll, filepath.Join(t.TempDir(), "out"), "feature", false, "info"); exitCode(err) != 1 {
			t.Errorf("collection, %s: err = %v, want exit 1", name, err)
		}
	}
}

func TestPackageEmptyLegacyIDsAddsNoCurrentID(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a", "devcontainer-feature.json"), `{"id":"a","version":"1.0.0","name":"A","legacyIds":[]}`, 0o644)
	writeFile(t, filepath.Join(src, "a", "install.sh"), "#!/bin/sh\n", 0o755)
	out := filepath.Join(t.TempDir(), "out")
	if err := packageCollection(src, out, "feature", false, "info"); err != nil {
		t.Fatal(err)
	}
	if got := readCollection(t, out); strings.Contains(got, "currentId") {
		t.Errorf("currentId added for empty legacyIds:\n%s", got)
	}
}

func TestPackageTemplateOptionalPathsShape(t *testing.T) {
	src := t.TempDir()
	tpl := filepath.Join(src, "tpl")
	writeFile(t, filepath.Join(tpl, "devcontainer-template.json"), `{"id":"tpl","version":"1.0.0","name":"T","optionalPaths":["z/*","abcd"]}`, 0o644)
	writeFile(t, filepath.Join(tpl, ".devcontainer.json"), `{"image":"x"}`, 0o644)
	writeFile(t, filepath.Join(tpl, "z", "only.txt"), "x\n", 0o644)
	writeFile(t, filepath.Join(tpl, "abc.txt"), "x\n", 0o644)
	out := filepath.Join(t.TempDir(), "out")
	if err := packageCollection(src, out, "template", false, "info"); err != nil {
		t.Fatal(err)
	}
	want := `"optionalPaths": [
                "z/only.txt",
                "abcd"
            ]`
	if got := readCollection(t, out); !strings.Contains(got, want) {
		t.Errorf("collection lacks %s:\n%s", want, got)
	}
}
