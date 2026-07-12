package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// writeTarGz builds a .tar.gz from name->content entries (a name ending in "/" is
// a directory entry) and returns its path.
func writeTarGz(t *testing.T, entries [][2]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		name, content := e[0], e[1]
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if len(name) > 0 && name[len(name)-1] == '/' {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()

	path := filepath.Join(t.TempDir(), "archive.tgz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractTarGz_Normal(t *testing.T) {
	arc := writeTarGz(t, [][2]string{
		{"devcontainer-feature.json", `{"id":"x"}`},
		{"sub/", ""},
		{"sub/install.sh", "#!/bin/sh\n"},
	})
	dest := t.TempDir()
	if err := extractTarGz(arc, dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "devcontainer-feature.json")); err != nil || string(b) != `{"id":"x"}` {
		t.Errorf("top file wrong: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "install.sh")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
	// extractTarGz removes the archive on success.
	if _, err := os.Stat(arc); !os.IsNotExist(err) {
		t.Errorf("archive should have been removed, stat err = %v", err)
	}
}

// TestExtractTarGz_ZipSlip proves the path-traversal guard: an entry that would
// escape the destination directory is rejected and no file is written outside.
func TestExtractTarGz_ZipSlip(t *testing.T) {
	arc := writeTarGz(t, [][2]string{
		{"../escaped.txt", "pwned"},
	})
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	err := extractTarGz(arc, dest)
	if err == nil {
		t.Fatal("expected a zip-slip rejection, got nil")
	}
	// The escaping file must NOT have been created in the parent.
	if _, statErr := os.Stat(filepath.Join(parent, "escaped.txt")); statErr == nil {
		t.Error("zip-slip guard failed: a file was written outside the destination")
	}
}
