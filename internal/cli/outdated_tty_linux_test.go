//go:build linux

package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

type terminalStdout struct{ terminal *os.File }

func (o terminalStdout) Stdout() io.Writer { return o.terminal }
func (terminalStdout) Stderr() io.Writer   { return io.Discard }

// outdated indents its JSON only on a terminal, as TS's
// JSON.stringify(outdated, undefined, process.stdout.isTTY ? '  ' : undefined);
// a character device that is no terminal (/dev/null) or a regular file gets
// none.
func TestOutdatedIndentsOnlyOnATerminal(t *testing.T) {
	master, terminal := openPTY(t)
	ws := writeOutdatedWorkspace(t, `{}`, "")
	if err := runOutdated(t.Context(), terminalStdout{terminal}, outdatedOpts{workspaceFolder: ws, outputFormat: "json", logLevel: "info"}); err != nil {
		t.Fatal(err)
	}
	want := "{\r\n  \"features\": {}\r\n}\r\n" // the terminal turns \n into \r\n
	// With the terminal side closed, the master reads what was written and
	// then fails (EIO) instead of waiting for more.
	terminal.Close()
	got, _ := io.ReadAll(master)
	if string(got) != want {
		t.Errorf("terminal output = %q, want %q", got, want)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	regular, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	for _, w := range []*os.File{devNull, regular} {
		if got := outdatedIndent(w); got != "" {
			t.Errorf("%s: indent = %q, want none", w.Name(), got)
		}
	}
}
