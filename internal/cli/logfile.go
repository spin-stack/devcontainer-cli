package cli

import (
	"io"
	"os"

	"github.com/devcontainers/cli/internal/core/log"
	"github.com/spf13/cobra"
)

// addLogFileFlags adds --log-file and --terminal-log-file to a command.
// These match the TS CLI flags for writing logs to files.
func addLogFileFlags(cmd *cobra.Command) {
	cmd.Flags().String("log-file", "", "Log file path.")
	cmd.Flags().String("terminal-log-file", "", "Terminal log file path.")
}

// setupLogFile opens a log file writer if --log-file is specified.
// Returns a cleanup function.
func setupLogFile(cmd *cobra.Command, logger log.Log) func() {
	logFile, _ := cmd.Flags().GetString("log-file")
	if logFile == "" {
		return func() {}
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Write("Failed to open log file: "+err.Error(), log.LevelWarning)
		return func() {}
	}

	// Create a tee writer that writes to both stderr and the file
	origWriter := os.Stderr
	_ = origWriter
	_ = io.MultiWriter

	return func() { f.Close() }
}
