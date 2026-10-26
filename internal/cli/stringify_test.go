package cli

import (
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

// A command's error result is written as JSON.stringify writes it.
func TestWriteErrorJSONDoesNotEscapeHTML(t *testing.T) {
	var out captureOutput
	_ = writeErrorJSON(&out, coreerrors.ErrorOutput{Outcome: "error", Message: "a && <b>", Description: "a && <b>"})
	want := `{"outcome":"error","message":"a && <b>","description":"a && <b>"}` + "\n"
	if got := out.out.String(); got != want {
		t.Errorf("writeErrorJSON wrote %q, want %q", got, want)
	}
}
