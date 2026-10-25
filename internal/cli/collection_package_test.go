package cli

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readCollection(t *testing.T, outDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, "devcontainer-collection.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// tarFile returns a file's content from a packaged .tgz.
func tarFile(t *testing.T, archive, name string) string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		// createTarArchive writes a plain tar, as node-tar does without gzip.
		f.Seek(0, io.SeekStart)
		return tarEntry(t, tar.NewReader(f), name)
	}
	return tarEntry(t, tar.NewReader(gz), name)
}

func tarEntry(t *testing.T, tr *tar.Reader, name string) string {
	t.Helper()
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not in archive: %v", name, err)
		}
		if h.Name == name {
			b, _ := io.ReadAll(tr)
			return string(b)
		}
	}
}

func exitCode(err error) int {
	if e, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok {
		return e.Code
	}
	return -1
}

// A collection is the folder's entries in name order, without those starting
// with '.'; one without devcontainer-feature.json is skipped (TS warns). A
// Feature with legacyIds gets currentId, in the collection and in the packaged
// devcontainer-feature.json, written as JSON.stringify(…, null, 4) (no HTML
// escaping of '&').
func TestPackageFeatureCollectionAsTS(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "b", "devcontainer-feature.json"), `{"id":"b","version":"1.0.0","name":"B & co", /* jsonc */ "legacyIds":["old-b"]}`, 0o644)
	writeFile(t, filepath.Join(src, "b", "install.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(src, "a", "devcontainer-feature.json"), `{"id":"a","version":"1.0.0","name":"A"}`, 0o644)
	writeFile(t, filepath.Join(src, "a", "install.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(src, ".hidden", "devcontainer-feature.json"), `{"id":"hidden","version":"1.0.0","name":"H"}`, 0o644)
	writeFile(t, filepath.Join(src, "no-metadata", "install.sh"), "#!/bin/sh\n", 0o755)
	out := filepath.Join(t.TempDir(), "out")

	if err := packageCollection(src, out, "feature", false, "info"); err != nil {
		t.Fatalf("package: %v", err)
	}
	want := `{
    "sourceInformation": {
        "source": "devcontainer-cli"
    },
    "features": [
        {
            "id": "a",
            "version": "1.0.0",
            "name": "A"
        },
        {
            "id": "b",
            "version": "1.0.0",
            "name": "B & co",
            "legacyIds": [
                "old-b"
            ],
            "currentId": "b"
        }
    ]
}`
	if got := readCollection(t, out); got != want {
		t.Errorf("collection:\n%s\nwant:\n%s", got, want)
	}
	if got := tarFile(t, filepath.Join(out, "devcontainer-feature-b.tgz"), "./devcontainer-feature.json"); !strings.Contains(got, `"currentId": "b"`) {
		t.Errorf("packaged devcontainer-feature.json lacks currentId:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(out, "devcontainer-feature-hidden.tgz")); err == nil {
		t.Error("a folder starting with '.' was packaged")
	}
	// The source is left as it was: the metadata is completed in a copy.
	if data, _ := os.ReadFile(filepath.Join(src, "b", "devcontainer-feature.json")); strings.Contains(string(data), "currentId") {
		t.Error("the source devcontainer-feature.json was rewritten")
	}
}

// What fails a TS package fails it here, with exit code 1: a Feature without
// install.sh, an item missing id/version/name (TS returns, it does not skip it),
// a collection with nothing to package - including a parent folder whose items
// are under src/, which TS does not descend into - and an output folder that
// exists without -f. A target that does not exist is an error.
func TestPackageFailuresAsTS(t *testing.T) {
	newOut := func() string { return filepath.Join(t.TempDir(), "out") }

	noInstall := t.TempDir()
	writeFile(t, filepath.Join(noInstall, "a", "devcontainer-feature.json"), `{"id":"a","version":"1.0.0","name":"A"}`, 0o644)
	if err := packageCollection(noInstall, newOut(), "feature", false, "info"); exitCode(err) != 1 {
		t.Errorf("missing install.sh: err = %v, want exit 1", err)
	}

	noName := t.TempDir()
	writeFile(t, filepath.Join(noName, "a", "devcontainer-feature.json"), `{"id":"a","version":"1.0.0"}`, 0o644)
	writeFile(t, filepath.Join(noName, "a", "install.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(noName, "b", "devcontainer-feature.json"), `{"id":"b","version":"1.0.0","name":"B"}`, 0o644)
	writeFile(t, filepath.Join(noName, "b", "install.sh"), "#!/bin/sh\n", 0o755)
	if err := packageCollection(noName, newOut(), "feature", false, "info"); exitCode(err) != 1 {
		t.Errorf("missing name: err = %v, want exit 1", err)
	}

	parent := t.TempDir()
	writeFile(t, filepath.Join(parent, "src", "a", "devcontainer-feature.json"), `{"id":"a","version":"1.0.0","name":"A"}`, 0o644)
	writeFile(t, filepath.Join(parent, "src", "a", "install.sh"), "#!/bin/sh\n", 0o755)
	if err := packageCollection(parent, newOut(), "feature", false, "info"); exitCode(err) != 1 {
		t.Errorf("parent of src: err = %v, want exit 1 (no descent into src/)", err)
	}

	existing := t.TempDir()
	if err := packageCollection(filepath.Join(parent, "src"), existing, "feature", false, "info"); exitCode(err) != 1 {
		t.Errorf("existing output without -f: err = %v, want exit 1", err)
	}
	if err := packageCollection(filepath.Join(parent, "src"), existing, "feature", true, "info"); err != nil {
		t.Errorf("existing output with -f: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "nope")
	if err := packageCollection(missing, newOut(), "feature", false, "info"); err == nil || err.Error() != "Target folder '"+missing+"' does not exist" {
		t.Errorf("missing target: err = %v", err)
	}
}

// A Template's metadata gains type, files (a folder's own files before its
// sub-folders'), fileCount and featureIds (the resources of its OCI Features, in
// the config's order), and an optionalPaths "dir/*" whose folder holds one file
// names that file - TS addsAdditionalTemplateProps.
func TestPackageTemplateAddsTSProps(t *testing.T) {
	src := t.TempDir()
	tpl := filepath.Join(src, "tpl")
	writeFile(t, filepath.Join(tpl, "devcontainer-template.json"), `{"id":"tpl","version":"1.0.0","name":"T","optionalPaths":[".github/*","docs/*"]}`, 0o644)
	writeFile(t, filepath.Join(tpl, ".devcontainer", "devcontainer.json"), `{"image":"x","features":{"ghcr.io/Owner/Features/z:1":{},"./local":{},"ghcr.io/owner/features/a":{}}}`, 0o644)
	writeFile(t, filepath.Join(tpl, ".github", "workflow.yml"), "on: push\n", 0o644)
	writeFile(t, filepath.Join(tpl, "docs", "a.md"), "a\n", 0o644)
	writeFile(t, filepath.Join(tpl, "docs", "b.md"), "b\n", 0o644)
	writeFile(t, filepath.Join(tpl, "README.md"), "r\n", 0o644)
	out := filepath.Join(t.TempDir(), "out")

	if err := packageCollection(src, out, "template", false, "info"); err != nil {
		t.Fatalf("package: %v", err)
	}
	got := readCollection(t, out)
	for _, want := range []string{
		`"type": "image"`,
		`"fileCount": 6`,
		`"featureIds": [
                "ghcr.io/owner/features/z",
                "ghcr.io/owner/features/a"
            ]`,
		`"optionalPaths": [
                ".github/workflow.yml",
                "docs/*"
            ]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("collection lacks %s:\n%s", want, got)
		}
	}
	files := templateFiles(tpl, "")
	want := []any{"README.md", "devcontainer-template.json", ".devcontainer/devcontainer.json", ".github/workflow.yml", "docs/a.md", "docs/b.md"}
	if !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}

	noConfig := t.TempDir()
	writeFile(t, filepath.Join(noConfig, "tpl", "devcontainer-template.json"), `{"id":"tpl","version":"1.0.0","name":"T"}`, 0o644)
	if err := packageCollection(noConfig, filepath.Join(t.TempDir(), "out"), "template", false, "info"); exitCode(err) != 1 {
		t.Errorf("template without devcontainer.json: err = %v, want exit 1", err)
	}
}

// writeCollectionMetadata surfaces a failed write instead of a false success.
func TestWriteCollectionMetadata_WriteError(t *testing.T) {
	if err := writeCollectionMetadata(filepath.Join(t.TempDir(), "does-not-exist"), "feature", nil); err == nil {
		t.Fatal("expected an error writing into a missing directory")
	}
}
