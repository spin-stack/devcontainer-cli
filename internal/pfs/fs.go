package pfs

import (
	"os"
	"path/filepath"
)

type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	MkdirAll(path string) error
	Stat(path string) (os.FileInfo, error)
	Walk(root string, fn filepath.WalkFunc) error
	Remove(path string, recursive bool) error
}

type OSFS struct{}

func (OSFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (OSFS) WriteFile(path string, data []byte) error     { return os.WriteFile(path, data, 0o644) }
func (OSFS) MkdirAll(path string) error                   { return os.MkdirAll(path, 0o755) }
func (OSFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OSFS) Walk(root string, fn filepath.WalkFunc) error { return filepath.Walk(root, fn) }

func (OSFS) Remove(path string, recursive bool) error {
	if recursive {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func DefaultFS() FS { return OSFS{} }

var _ FS = OSFS{}
