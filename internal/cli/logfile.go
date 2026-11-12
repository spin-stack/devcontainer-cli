package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

func addLogFileFlags(cmd *cobra.Command, logFile, terminalLogFile *string) {
	cmd.Flags().StringVar(logFile, "log-file", "", "Log file path. When set, logs are written to this file in addition to stderr.")
	cmd.Flags().StringVar(terminalLogFile, "terminal-log-file", "", "Terminal log file path. Captures the same log stream as --log-file (the CLI has no separate terminal stream).")
}

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
		if dir := filepath.Dir(p); dir != "" && dir != "." { // mutate-exempt: MkdirAll of "." creates nothing
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			cleanup()
			return os.Stderr, noop, err
		}
		files = append(files, f)
		writers = append(writers, plainFileWriter{f})
	}

	if len(files) == 0 {
		return os.Stderr, noop, nil
	}
	return io.MultiWriter(writers...), cleanup, nil
}

type plainFileWriter struct{ w io.Writer }

func (p plainFileWriter) Write(b []byte) (int, error) {
	if _, err := io.WriteString(p.w, log.StripTerminalFormatting(string(b))); err != nil {
		return 0, err
	}
	return len(b), nil
}
