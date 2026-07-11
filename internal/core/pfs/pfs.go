package pfs

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractTarGz extracts a tar archive (gzip-compressed or plain) into destDir.
// Entries that would escape destDir (path traversal) are skipped. The archive
// file at archivePath is removed on success.
func ExtractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}

	var reader io.Reader
	if gzr, gzErr := gzip.NewReader(f); gzErr == nil {
		reader = gzr
		defer gzr.Close()
		defer f.Close()
	} else {
		// Not gzip — reopen as a plain tar stream.
		f.Close()
		f2, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f2.Close()
		reader = f2
	}

	cleanDest := filepath.Clean(destDir)
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		target := filepath.Join(cleanDest, filepath.Clean("/"+header.Name))
		// Guard against path traversal (../ entries escaping destDir).
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			if header.Mode != 0 {
				os.Chmod(target, os.FileMode(header.Mode))
			}
		}
	}
	os.Remove(archivePath)
	return nil
}

// ReadFile reads the contents of the file at path.
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes data to path, creating or truncating it.
func WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// MkdirAll creates a directory path and all parents.
func MkdirAll(path string) error {
	return os.MkdirAll(path, 0755)
}

// Remove removes path. If recursive, removes all children.
func Remove(path string, recursive bool) error {
	if recursive {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// IsFile returns true if path exists and is a regular file.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// IsDir returns true if path exists and is a directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Exists returns true if path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Resolve resolves a relative path against a base.
func Resolve(base string, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}
