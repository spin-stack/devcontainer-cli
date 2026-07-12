package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Output is the seam for a command's stdout/stderr. Routing every result and
// progress write through it lets tests capture output with an injected buffer
// instead of swapping the global os.Stdout (which is racy and defeats parallel
// tests). It intentionally exposes only the two writers consumers use.
type Output interface {
	Stdout() io.Writer
	Stderr() io.Writer
}

// osOutput writes to the process's real stdout/stderr. It is the default when no
// command-scoped Output is available.
type osOutput struct{}

func (osOutput) Stdout() io.Writer { return os.Stdout }
func (osOutput) Stderr() io.Writer { return os.Stderr }

// OSOutput returns the default Output backed by os.Stdout/os.Stderr.
func OSOutput() Output { return osOutput{} }

// cmdOutput adapts a *cobra.Command to Output, reusing its OutOrStdout /
// ErrOrStderr. Those default to os.Stdout/os.Stderr but can be overridden in
// tests via cmd.SetOut / cmd.SetErr, giving a race-free capture point.
type cmdOutput struct{ cmd *cobra.Command }

func (c cmdOutput) Stdout() io.Writer { return c.cmd.OutOrStdout() }
func (c cmdOutput) Stderr() io.Writer { return c.cmd.ErrOrStderr() }

// outputFor returns an Output backed by the given cobra command.
func outputFor(cmd *cobra.Command) Output { return cmdOutput{cmd: cmd} }

// progressWriter returns the writer that live subprocess progress (e.g. docker
// build) should stream to: w in text mode, or nil under --log-format json where
// a raw byte stream would corrupt the structured event stream (callers treat a
// nil writer as "buffer and emit as a log event instead").
func progressWriter(logFormat string, w io.Writer) io.Writer {
	if logFormat == "json" {
		return nil
	}
	return w
}
