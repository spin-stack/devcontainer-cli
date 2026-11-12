package features

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadLockfileRefusesTooDeepJSON(t *testing.T) {
	dir := t.TempDir()
	deep := strings.Repeat("[", 10001) + strings.Repeat("]", 10001)
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-lock.json"), []byte(deep), 0o644); err != nil {
		t.Fatal(err)
	}
	lockfile, initLockfile, err := ReadLockfile(filepath.Join(dir, "devcontainer.json"))
	if err == nil || lockfile != nil || initLockfile {
		t.Errorf("ReadLockfile = %v, %v, %v; want the decoding error", lockfile, initLockfile, err)
	}
}

func TestReadLockfileUnreadableIsNodesEACCES(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "devcontainer-lock.json")
	if err := os.WriteFile(lockPath, []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadLockfile(filepath.Join(dir, "devcontainer.json"))
	lerr, ok := err.(*LockfileError)
	if !ok {
		t.Fatalf("err = %#v, want a *LockfileError", err)
	}
	if want := "EACCES: permission denied, open '" + lockPath + "'"; lerr.Error() != want {
		t.Errorf("message = %q, want %q", lerr.Error(), want)
	}
	want := "[Error: EACCES: permission denied, open '" + lockPath + "'] {\n  errno: -13,\n  code: 'EACCES',\n  syscall: 'open',\n  path: '" + lockPath + "'\n}"
	if got := lerr.Inspect(); got != want {
		t.Errorf("Inspect() =\n%s\nwant\n%s", got, want)
	}
}

func TestLockfileErrorForms(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "devcontainer-lock.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadLockfile(filepath.Join(dir, "devcontainer.json"))
	lerr, ok := err.(*LockfileError)
	if !ok {
		t.Fatalf("err = %#v, want a *LockfileError", err)
	}
	if got, want := lerr.Inspect(), "[Error: EISDIR: illegal operation on a directory, read] {\n  errno: -21,\n  code: 'EISDIR',\n  syscall: 'read'\n}"; got != want {
		t.Errorf("Inspect() =\n%s\nwant\n%s", got, want)
	}
	if got, want := lerr.Stack(), "Error: EISDIR: illegal operation on a directory, read"; got != want {
		t.Errorf("Stack() = %q, want %q", got, want)
	}

	syntax := &LockfileError{Name: "SyntaxError", Message: "Unexpected end of JSON input"}
	if got, want := syntax.Inspect(), "SyntaxError: Unexpected end of JSON input"; got != want {
		t.Errorf("SyntaxError Inspect() = %q, want %q", got, want)
	}
}

func TestJSQuote(t *testing.T) {
	for in, want := range map[string]string{
		`/a/b`:      `'/a/b'`,
		`/a'b`:      `"/a'b"`,
		`/a'b"c`:    "`/a'b\"c`",
		"/a'b\"c`d": `'/a\'b"c` + "`" + `d'`,
		`/a\b`:      `'/a\\b'`,
	} {
		if got := jsQuote(in); got != want {
			t.Errorf("jsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestNodeFSErrorKeepsAnUnnamedError(t *testing.T) {
	plain := os.ErrClosed
	if got := nodeFSError(plain); got != plain {
		t.Errorf("nodeFSError(%v) = %v, want it unchanged", plain, got)
	}
	unnamed := &os.PathError{Op: "read", Path: "/x", Err: syscall.EAGAIN}
	if got := nodeFSError(unnamed); got != error(unnamed) {
		t.Errorf("nodeFSError(%v) = %v, want it unchanged", unnamed, got)
	}
}
