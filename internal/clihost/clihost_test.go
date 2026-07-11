package clihost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewLocal(t *testing.T) {
	h, err := NewLocal("")
	if err != nil {
		t.Fatal(err)
	}

	if h.Cwd() == "" {
		t.Error("cwd should not be empty")
	}

	switch runtime.GOOS {
	case "darwin":
		if h.Platform() != "darwin" {
			t.Errorf("platform = %q", h.Platform())
		}
	case "linux":
		if h.Platform() != "linux" {
			t.Errorf("platform = %q", h.Platform())
		}
	}

	switch runtime.GOARCH {
	case "amd64":
		if h.Arch() != "x64" {
			t.Errorf("arch = %q, want x64", h.Arch())
		}
	case "arm64":
		if h.Arch() != "arm64" {
			t.Errorf("arch = %q", h.Arch())
		}
	}

	if h.HomeDir() == "" {
		t.Error("homedir should not be empty")
	}
	if h.TmpDir() == "" {
		t.Error("tmpdir should not be empty")
	}
	if h.Username() == "" {
		t.Error("username should not be empty")
	}
}

func TestLocal_Filesystem(t *testing.T) {
	dir := t.TempDir()
	h, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "test.txt")
	if err := h.WriteFile(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if !h.IsFile(path) {
		t.Error("expected IsFile true")
	}
	data, err := h.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q", string(data))
	}

	subdir := filepath.Join(dir, "sub")
	if err := h.MkdirAll(subdir); err != nil {
		t.Fatal(err)
	}
	if !h.IsDir(subdir) {
		t.Error("expected IsDir true")
	}
}

func TestLocal_Exec(t *testing.T) {
	h, err := NewLocal("")
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.Exec(ExecParams{Cmd: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d", res.ExitCode)
	}
	if got := string(res.Stdout); got != "hello\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestLocal_ExecNonZero(t *testing.T) {
	h, err := NewLocal("")
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.Exec(ExecParams{Cmd: "sh", Args: []string{"-c", "exit 42"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", res.ExitCode)
	}
}

func TestLocal_Env(t *testing.T) {
	h, err := NewLocal("")
	if err != nil {
		t.Fatal(err)
	}
	env := h.Env()
	if _, ok := env["PATH"]; !ok {
		t.Error("PATH not in env")
	}
}

func TestPlatformMapping(t *testing.T) {
	if goOSToNodePlatform("windows") != "win32" {
		t.Error("windows → win32")
	}
	if goOSToNodePlatform("linux") != "linux" {
		t.Error("linux → linux")
	}
	if goArchToNodeArch("amd64") != "x64" {
		t.Error("amd64 → x64")
	}
	if goArchToNodeArch("arm64") != "arm64" {
		t.Error("arm64 → arm64")
	}
}

func TestReverseMapping(t *testing.T) {
	if PlatformToGOOS("win32") != "windows" {
		t.Error("win32 → windows")
	}
	if ArchToGOARCH("x64") != "amd64" {
		t.Error("x64 → amd64")
	}
}

func TestEnvToMap(t *testing.T) {
	m := envToMap([]string{"A=1", "B=2=3", "C="})
	if m["A"] != "1" {
		t.Errorf("A = %q", m["A"])
	}
	if m["B"] != "2=3" {
		t.Errorf("B = %q (should preserve = in value)", m["B"])
	}
	if m["C"] != "" {
		t.Errorf("C = %q", m["C"])
	}
}

func TestNewLocal_WithCwd(t *testing.T) {
	dir := t.TempDir()
	h, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Cwd() != dir {
		t.Errorf("cwd = %q, want %q", h.Cwd(), dir)
	}
	_ = os.Chdir
}
