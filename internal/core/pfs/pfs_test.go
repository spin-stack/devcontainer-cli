package pfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	err := WriteFile(path, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	data, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q", string(data))
	}
}

func TestMkdirAll(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")

	if err := MkdirAll(nested); err != nil {
		t.Fatal(err)
	}
	if !IsDir(nested) {
		t.Error("directory not created")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("x"), 0644)

	if err := Remove(path, false); err != nil {
		t.Fatal(err)
	}
	if Exists(path) {
		t.Error("file still exists")
	}
}

func TestRemoveRecursive(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub")
	os.MkdirAll(filepath.Join(nested, "deep"), 0755)
	os.WriteFile(filepath.Join(nested, "deep", "f.txt"), []byte("x"), 0644)

	if err := Remove(nested, true); err != nil {
		t.Fatal(err)
	}
	if Exists(nested) {
		t.Error("directory still exists")
	}
}

func TestIsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("x"), 0644)

	if !IsFile(path) {
		t.Error("expected true for file")
	}
	if IsFile(dir) {
		t.Error("expected false for directory")
	}
	if IsFile(filepath.Join(dir, "nonexistent")) {
		t.Error("expected false for nonexistent")
	}
}

func TestResolve(t *testing.T) {
	got := Resolve("/home/user", "project")
	want := filepath.Join("/home/user", "project")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	abs := Resolve("/home/user", "/absolute/path")
	if abs != "/absolute/path" {
		t.Errorf("got %q for absolute", abs)
	}
}
