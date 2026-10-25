package pfs

import (
	"os"
	"path/filepath"
)

// FS is the seam over the filesystem operations the CLI performs while applying
// templates and writing workspaces. It lists EXACTLY the methods consumers use
// (ReadFile/WriteFile/MkdirAll/Stat/Walk/Remove) so a fake can drive failure
// paths (e.g. a WriteFile that fails mid-apply) without touching real disk.
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	MkdirAll(path string) error
	Stat(path string) (os.FileInfo, error)
	Walk(root string, fn filepath.WalkFunc) error
	Remove(path string, recursive bool) error
}

// OSFS is the default FS, backed by the real filesystem.
type OSFS struct{}

func (OSFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (OSFS) WriteFile(path string, data []byte) error     { return os.WriteFile(path, data, 0o644) }
func (OSFS) MkdirAll(path string) error                   { return os.MkdirAll(path, 0o755) }
func (OSFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OSFS) Walk(root string, fn filepath.WalkFunc) error { return filepath.Walk(root, fn) }

// Remove removes path; recursive removes its children too.
func (OSFS) Remove(path string, recursive bool) error {
	if recursive {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// DefaultFS returns the default OS-backed FS.
func DefaultFS() FS { return OSFS{} }

// Compile-time assertion that OSFS implements FS.
var _ FS = OSFS{}
