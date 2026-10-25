//go:build unix

package pfs

import (
	"archive/tar"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// writeEntries writes a plain tar holding headers in order, each regular file
// with body as content.
func writeEntries(t *testing.T, body string, headers ...tar.Header) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, h := range headers {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestExtractTarGzModes(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	src := writeEntries(t, "x",
		tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o700},
		tar.Header{Name: "nested/deep/file", Typeflag: tar.TypeReg, Mode: 0o600},
		tar.Header{Name: "modeless", Typeflag: tar.TypeReg, Mode: 0},
		tar.Header{Name: "script.sh", Typeflag: tar.TypeReg, Mode: 0o751},
	)
	dest := t.TempDir()
	if err := ExtractTarGz(src, dest); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]os.FileMode{
		"dir":              0o755, // directories are created 0755, not with the entry's mode
		"nested/deep":      0o755,
		"nested/deep/file": 0o600,
		"modeless":         0o644, // no mode in the header: os.Create's default stays
		"script.sh":        0o751,
	} {
		if got := perm(t, filepath.Join(dest, rel)); got != want {
			t.Errorf("%s mode = %v, want %v", rel, got, want)
		}
	}
}

func TestExtractTarGzRefusesARegularFileAtTheRoot(t *testing.T) {
	// "." names destDir itself; a regular file cannot replace the directory,
	// and the extraction must say so instead of dropping the entry.
	src := writeEntries(t, "x", tar.Header{Name: ".", Typeflag: tar.TypeReg, Mode: 0o644})
	if err := ExtractTarGz(src, t.TempDir()); err == nil {
		t.Fatal("ExtractTarGz succeeded writing a regular file over destDir")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("a failed extraction must keep the archive: %v", err)
	}
}
