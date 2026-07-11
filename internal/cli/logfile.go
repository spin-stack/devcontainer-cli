package cli

import (
	"github.com/spf13/cobra"
)

// addLogFileFlags adds --log-file and --terminal-log-file to a command.
// These match the TS CLI flags for writing logs to files.
//
// NOTE: the flags are accepted for CLI-surface parity but not yet wired to
// actually write logs to a file (tracked in REMAINING-WORK.md, RW-014).
func addLogFileFlags(cmd *cobra.Command) {
	cmd.Flags().String("log-file", "", "Log file path.")
	cmd.Flags().String("terminal-log-file", "", "Terminal log file path.")
}
