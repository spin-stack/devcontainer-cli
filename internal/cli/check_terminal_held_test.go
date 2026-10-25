package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A character device, as a terminal is, counts as a terminal writer; a regular
// file does not.
func TestIsTerminalWriterChecksTheFileMode(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	if !isTerminalWriter(devNull) {
		t.Errorf("%s is a character device: want a terminal writer", os.DevNull)
	}
	regular, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if isTerminalWriter(regular) {
		t.Error("a regular file is not a terminal writer")
	}
}
