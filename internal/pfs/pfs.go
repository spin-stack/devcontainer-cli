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
		f.Close()
		f2, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f2.Close()
		reader = f2
	}

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		name, ok := entryPath(header.Name)
		if !ok {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
				return err
			}
			out, err := root.Create(name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			if header.Mode != 0 {
				if err := root.Chmod(name, os.FileMode(header.Mode)); err != nil {
					return err
				}
			}
		}
	}
	os.Remove(archivePath)
	return nil
}

// entryPath is where an entry named name extracts, relative to the
// destination, and false for an entry node-tar skips. node-tar (unpack.js
// STRIPABSOLUTEPATH) strips the root of an absolute path, Windows roots
// included on every platform ('/', '\', a drive letter), and refuses a path
// with a ".." segment, splitting on both separators.
func entryPath(name string) (string, bool) {
	for {
		stripped := strings.TrimLeft(name, `/\`)
		if len(stripped) >= 2 && stripped[1] == ':' && isASCIILetter(stripped[0]) {
			stripped = stripped[2:]
		}
		if stripped == name {
			break
		}
		name = stripped
	}
	for segment := range strings.FieldsFuncSeq(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return "", false
		}
	}
	return filepath.Clean(name), true
}

func isASCIILetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func IsFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func IsDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func Resolve(base string, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}
