//go:build unix

package pfs

import (
	"archive/tar"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sandbox(t *testing.T) (parent, dest string) {
	t.Helper()
	parent = t.TempDir()
	dest = filepath.Join(parent, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	return parent, dest
}

func tree(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

func reg(name string) tar.Header {
	return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}
}

func TestExtractTarGzSkipsDotDotEntries(t *testing.T) {
	parent, dest := sandbox(t)
	src := writeEntries(t, "x",
		reg("../up.txt"), reg("a/../mid.txt"), reg(`a\..\back.txt`), reg(`..\win.txt`),
		tar.Header{Name: "../updir/", Typeflag: tar.TypeDir, Mode: 0o755},
		reg("sub/..x"), reg("ok.txt"),
	)
	if err := ExtractTarGz(src, dest); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, dest), []string{"ok.txt", "sub", "sub/..x"}; !slices.Equal(got, want) {
		t.Errorf("destDir holds %q, want %q", got, want)
	}
	if got := tree(t, parent); !slices.Equal(got, append([]string{"dest"}, prefixed("dest", tree(t, dest))...)) {
		t.Errorf("the extraction wrote beside destDir: %q", got)
	}
}

func prefixed(dir string, paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Join(dir, p)
	}
	return out
}

func TestExtractTarGzStripsAbsolutePaths(t *testing.T) {
	parent, dest := sandbox(t)
	src := writeEntries(t, "x",
		reg("/abs.txt"), reg("//abs2.txt"), reg(`\back.txt`), reg("c:/drive.txt"), reg("C:rel.txt"),
		reg("/c:/nested/x.txt"), reg("1:digit.txt"),
		reg("a:a.txt"), reg("z:z.txt"), reg("A:A2.txt"), reg("Z:Z2.txt"),
		reg("`:tick.txt"), reg("{:brace.txt"), reg("@:at.txt"), reg("[:bracket.txt"),
		tar.Header{Name: "d:", Typeflag: tar.TypeDir, Mode: 0o755},
	)
	if err := ExtractTarGz(src, dest); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1:digit.txt", "@:at.txt", "A2.txt", "Z2.txt", "[:bracket.txt", "`:tick.txt", "a.txt",
		"abs.txt", "abs2.txt", "back.txt", "drive.txt", "nested", "nested/x.txt", "rel.txt", "z.txt", "{:brace.txt",
	}
	if got := tree(t, dest); !slices.Equal(got, want) {
		t.Errorf("destDir holds %q, want %q", got, want)
	}
	if got := tree(t, parent); len(got) != len(want)+1 {
		t.Errorf("the extraction wrote beside destDir: %q", got)
	}
}

func TestExtractTarGzCannotWriteThroughASymlink(t *testing.T) {
	for name, entry := range map[string]tar.Header{
		"file":      reg("link/pwned.txt"),
		"directory": {Name: "link/pwned/", Typeflag: tar.TypeDir, Mode: 0o755},
		"nested":    reg("link/deeper/pwned.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			parent, dest := sandbox(t)
			outside := filepath.Join(parent, "outside")
			if err := os.Mkdir(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dest, "link")); err != nil {
				t.Fatal(err)
			}
			src := writeEntries(t, "x", entry)
			err := ExtractTarGz(src, dest)
			if err == nil || !strings.Contains(err.Error(), "escapes") {
				t.Errorf("err = %v, want the path escape refused", err)
			}
			if got := tree(t, outside); len(got) != 0 {
				t.Errorf("the extraction wrote outside destDir: %q", got)
			}
			if _, statErr := os.Stat(src); statErr != nil {
				t.Errorf("a failed extraction must keep the archive: %v", statErr)
			}
		})
	}
}

func TestExtractTarGzDoesNotCreateSymlinkEntries(t *testing.T) {
	parent, dest := sandbox(t)
	src := writeEntries(t, "x",
		tar.Header{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: parent},
		reg("evil/x.txt"),
	)
	if err := ExtractTarGz(src, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dest, "evil"))
	if err != nil || !info.IsDir() {
		t.Fatalf("evil = %v, %v; want a plain directory", info, err)
	}
	if got := tree(t, parent); !slices.Equal(got, []string{"dest", "dest/evil", "dest/evil/x.txt"}) {
		t.Errorf("parent holds %q", got)
	}
}
