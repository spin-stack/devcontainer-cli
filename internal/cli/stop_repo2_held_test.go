package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// The commands that take an optional positional path refuse a second one, as
// the TS CLI's yargs parser (.strict()) refuses arguments its commands do not
// declare; nothing is run.
func TestPathCommandsRefuseASecondPositional(t *testing.T) {
	for name, newCmd := range map[string]func() *cobra.Command{
		"stop":                    newStopCmd,
		"down":                    newDownCmd,
		"open":                    newOpenCmd,
		"features generate-docs":  realFeaturesGenerateDocsCmd,
		"templates generate-docs": realTemplatesGenerateDocsCmd,
		"features test":           realFeaturesTestCmd,
	} {
		cmd := newCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{t.TempDir(), t.TempDir()})
		err := cmd.ExecuteContext(t.Context())
		if want := "accepts at most 1 arg(s), received 2"; err == nil || err.Error() != want || out.Len() != 0 {
			t.Errorf("%s with two paths: err = %v, stdout = %q; want %q and no output", name, err, out.String(), want)
		}
	}
}
