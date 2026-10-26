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

// A log file holds the log without the terminal format: on a terminal stdout
// the log is colored and ends its lines in \r\n (TS createTerminalLog), and
// the file keeps only the text, as --log-file and --terminal-log-file document.
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

// A log file that fails to take a line reports the failure, not a write.
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
