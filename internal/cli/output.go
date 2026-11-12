package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

type Output interface {
	Stdout() io.Writer
	Stderr() io.Writer
}

type osOutput struct{}

func (osOutput) Stdout() io.Writer { return os.Stdout }
func (osOutput) Stderr() io.Writer { return os.Stderr }

func OSOutput() Output { return osOutput{} }

type cmdOutput struct{ cmd *cobra.Command }

func (c cmdOutput) Stdout() io.Writer { return c.cmd.OutOrStdout() }
func (c cmdOutput) Stderr() io.Writer { return c.cmd.ErrOrStderr() }

func outputFor(cmd *cobra.Command) Output { return cmdOutput{cmd: cmd} }

func progressWriter(logFormat string, w io.Writer) io.Writer {
	if logFormat == "json" {
		return nil
	}
	return w
}
