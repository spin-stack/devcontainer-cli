package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// TestLogWriterTeesToFile is the hermetic contract test for --log-file: a log
// line emitted through a logger built on the returned writer must land in the
// file (in addition to stderr).
func TestLogWriterTeesToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "run.log") // nested dir exercises MkdirAll

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

// TestLogWriterTeesToBothFiles confirms both --log-file and --terminal-log-file
// paths receive the same log stream (the CLI has a single log stream).
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

// TestLogWriterNoFileFallsBackToStderr confirms that with no paths the writer is
// os.Stderr and the cleanup is a harmless no-op.
func TestLogWriterNoFileFallsBackToStderr(t *testing.T) {
	w, cleanup, err := logWriter("", "")
	if err != nil {
		t.Fatalf("logWriter: %v", err)
	}
	if w != os.Stderr {
		t.Fatalf("writer = %v, want os.Stderr when no log file requested", w)
	}
	cleanup() // must not panic
}

// TestLogWriterOpenError surfaces an error (rather than silently dropping logs)
// when the log file path cannot be opened, and falls back to os.Stderr.
func TestLogWriterOpenError(t *testing.T) {
	dir := t.TempDir()
	// A path whose parent is a regular file cannot be created as a directory.
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
	cleanup() // must not panic
}
