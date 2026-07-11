package pfs

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTar(t *testing.T, gzip bool, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.tar")
	f, _ := os.Create(path)
	var tw *tar.Writer
	var gz *gzipWriterCloser
	if gzip {
		gz = newGzip(f)
		tw = tar.NewWriter(gz.w)
	} else {
		tw = tar.NewWriter(f)
	}
	for name, content := range entries {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	if gz != nil {
		gz.w.Close()
	}
	f.Close()
	return path
}

type gzipWriterCloser struct{ w *gzip.Writer }

func newGzip(f *os.File) *gzipWriterCloser { return &gzipWriterCloser{w: gzip.NewWriter(f)} }

func TestExtractTarGz_GzipAndPlain(t *testing.T) {
	for _, gz := range []bool{true, false} {
		src := writeTar(t, gz, map[string]string{
			"devcontainer-feature.json": `{"id":"x"}`,
			"sub/install.sh":            "echo hi",
		})
		dest := t.TempDir()
		if err := ExtractTarGz(src, dest); err != nil {
			t.Fatalf("gzip=%v: %v", gz, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "devcontainer-feature.json")); string(b) != `{"id":"x"}` {
			t.Errorf("gzip=%v: feature.json = %q", gz, b)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "sub/install.sh")); string(b) != "echo hi" {
			t.Errorf("gzip=%v: install.sh = %q", gz, b)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("gzip=%v: archive should be removed after extract", gz)
		}
	}
}

func TestExtractTarGz_PathTraversalSkipped(t *testing.T) {
	// A ../ entry must not escape destDir.
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.tar")
	f, _ := os.Create(path)
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "../escaped.txt", Mode: 0644, Size: 3, Typeflag: tar.TypeReg})
	tw.Write([]byte("bad"))
	tw.WriteHeader(&tar.Header{Name: "ok.txt", Mode: 0644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("ok"))
	tw.Close()
	f.Close()

	dest := t.TempDir()
	if err := ExtractTarGz(path, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); err == nil {
		t.Error("path traversal entry escaped destDir")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "ok.txt")); string(b) != "ok" {
		t.Errorf("ok.txt = %q", b)
	}
}
