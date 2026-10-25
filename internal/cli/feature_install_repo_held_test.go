package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A regular file keeps the mode its tar entry records, so a Feature's
// install.sh stays executable; an entry recording no mode keeps the mode the
// file was created with instead of becoming unreadable.
func TestExtractTarGzFileModes(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, mode := range map[string]int64{"install.sh": 0o750, "nomode.txt": 0} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: 2, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "feature.tar")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "install.sh")); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("install.sh: %v, %v, want mode 0750", fi.Mode(), err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "nomode.txt")); err != nil || string(b) != "hi" {
		t.Errorf("nomode.txt: %q, %v, want it readable", b, err)
	}
}

// A tarball download succeeds only on a 2xx answer, as TS treats only
// 200-299 as success: a 300 fails it.
func TestDownloadFeatureTarballStatus(t *testing.T) {
	for status, ok := range map[int]bool{200: true, 299: true, 300: false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("tgz"))
		}))
		data, _, err := downloadFeatureTarball(context.Background(), nil, srv.URL+"/f.tgz", nil)
		srv.Close()
		if ok && (err != nil || string(data) != "tgz") {
			t.Errorf("HTTP %d: %q, %v, want the body", status, data, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "HTTP 300")) {
			t.Errorf("HTTP %d: err = %v, want the status", status, err)
		}
	}
}
