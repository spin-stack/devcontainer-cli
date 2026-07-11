package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// addLogFileFlags adds --log-file and --terminal-log-file to a command, binding
// them to the given string targets. These match the TS CLI flag surface.
//
// --log-file is wired: when set, the command's log stream is teed to the file (see
// logWriter). --terminal-log-file is also teed to its path, but note the CLI keeps
// a single log stream — it has no self-managed PTY/terminal stream distinct from
// the log stream (RW-003 Branch A: exec inherits the terminal). So both flags
// capture the same, non-ANSI, log output; there is no separate terminal-formatted
// capture. This is a deliberate, documented divergence from the TS CLI, which
// produces distinct plain and terminal-formatted files.
func addLogFileFlags(cmd *cobra.Command, logFile, terminalLogFile *string) {
	cmd.Flags().StringVar(logFile, "log-file", "", "Log file path. When set, logs are written to this file in addition to stderr.")
	cmd.Flags().StringVar(terminalLogFile, "terminal-log-file", "", "Terminal log file path. Captures the same log stream as --log-file (the CLI has no separate terminal stream).")
}

// logWriter builds the io.Writer for a command's logger, teeing os.Stderr to each
// non-empty file path (--log-file, --terminal-log-file). It returns the writer and
// a cleanup func that closes any opened files; the cleanup is always non-nil and
// safe to defer. If a path cannot be opened, the error is returned and the writer
// falls back to os.Stderr (logs are never silently dropped).
func logWriter(paths ...string) (io.Writer, func(), error) {
	noop := func() {}
	writers := []io.Writer{os.Stderr}
	var files []*os.File
	cleanup := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}

	for _, p := range paths {
		if p == "" {
			continue
		}
		if dir := filepath.Dir(p); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			cleanup()
			return os.Stderr, noop, err
		}
		files = append(files, f)
		writers = append(writers, f)
	}

	if len(files) == 0 {
		return os.Stderr, noop, nil
	}
	return io.MultiWriter(writers...), cleanup, nil
}
