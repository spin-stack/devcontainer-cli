package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestLogWriterTeesToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "run.log")

	w, cleanup, err := logWriter(logPath, "")
	if err != nil {
		t.Fatalf("logWriter: %v", err)
	}

	logger := log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: w})
	logger.Write("hello from the log-file test", log.LevelInfo)
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello from the log-file test") {
		t.Fatalf("log file missing the emitted line, got:\n%s", string(data))
	}
}

func TestLogWriterTeesToBothFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.txt")
	termPath := filepath.Join(dir, "terminal.txt")

	w, cleanup, err := logWriter(logPath, termPath)
	if err != nil {
		t.Fatalf("logWriter: %v", err)
	}
	logger := log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: w})
	logger.Write("shared line", log.LevelInfo)
	cleanup()

	for _, p := range []string{logPath, termPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if !strings.Contains(string(data), "shared line") {
			t.Fatalf("%s missing emitted line, got:\n%s", p, string(data))
		}
	}
}

func TestLogWriterKeepsTheTerminalFormatOutOfTheFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log.txt")
	w, cleanup, err := logWriter(logPath, "")
	if err != nil {
		t.Fatalf("logWriter: %v", err)
	}
	line := "[\x1b[1m\x1b[38;2;99;143;79m12 ms\x1b[39m\x1b[22m] took \x1b[1m\x1b[38;2;86;156;214m5\x1b[39m\x1b[22m ms\r\nnext\r\n"
	if n, err := w.Write([]byte(line)); err != nil || n != len(line) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(line))
	}
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[12 ms] took 5 ms\nnext\n"; string(data) != want {
		t.Errorf("log file = %q, want %q", data, want)
	}
}

func TestPlainFileWriterReportsAFailedWrite(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if n, err := (plainFileWriter{f}).Write([]byte("line\n")); n != 0 || err == nil {
		t.Errorf("Write = %d, %v; want 0 and the error", n, err)
	}
}

func TestLogWriterNoFileFallsBackToStderr(t *testing.T) {
	w, cleanup, err := logWriter("", "")
	if err != nil {
		t.Fatalf("logWriter: %v", err)
	}
	if w != os.Stderr {
		t.Fatalf("writer = %v, want os.Stderr when no log file requested", w)
	}
	cleanup()
}

func TestLogWriterOpenError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(blocker, "child", "run.log")

	w, cleanup, err := logWriter(badPath, "")
	if err == nil {
		t.Fatal("expected error opening log file under a file path, got nil")
	}
	if w != os.Stderr {
		t.Fatalf("writer = %v, want os.Stderr fallback on error", w)
	}
	cleanup()
}
