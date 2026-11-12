package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestSecretMaskingReplacesEachSecretWhole(t *testing.T) {
	plainStdout(t)
	var buf bytes.Buffer
	l := New(Options{Level: LevelInfo, Format: "text", Writer: &buf, Secrets: []string{"", "tok", "tok3n"}})
	l.Write("use tok3n and tok", LevelInfo)
	if out := buf.String(); !strings.HasSuffix(out, " use ******** and ********\n") {
		t.Fatalf("output = %q", out)
	}
}
