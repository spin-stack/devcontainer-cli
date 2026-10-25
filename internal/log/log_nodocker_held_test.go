package log

import (
	"bytes"
	"strings"
	"testing"
)

// Every secret is replaced whole, the longer first, and an empty one masks
// nothing, as TS replaceAllLog (spec-utils/log.ts) filters out empty values and
// sorts the rest longest first.
func TestSecretMaskingReplacesEachSecretWhole(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: LevelInfo, Format: "text", Writer: &buf, Secrets: []string{"", "tok", "tok3n"}})
	l.Write("use tok3n and tok", LevelInfo)
	if out := buf.String(); !strings.HasSuffix(out, " use ******** and ********\n") {
		t.Fatalf("output = %q", out)
	}
}
