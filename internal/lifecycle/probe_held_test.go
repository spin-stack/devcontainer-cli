package lifecycle

import (
	"errors"
	"maps"
	"testing"
)

// A non-root user's merged PATH leaves out every base entry under an sbin
// directory, not only one ending in it: TS mergePaths tests /\/sbin(\/|$)/.
func TestMergePathsSkipsEntriesUnderSbinForNonRoot(t *testing.T) {
	if got := mergePaths("/usr/bin", "/opt/sbin/tools:/opt/sbinx:/usr/bin", false); got != "/opt/sbinx:/usr/bin" {
		t.Errorf("mergePaths = %q, want /opt/sbinx:/usr/bin", got)
	}
}

// The env cache is used only when it was read: an exec error, a non-zero exit
// or no output is a miss even when stdout holds JSON.
func TestReadEnvCacheMissesUnlessTheReadSucceeded(t *testing.T) {
	for name, reply := range map[string]struct {
		code int
		err  error
	}{
		"exec error":     {0, errors.New("gone")},
		"non-zero exit":  {1, nil},
		"error and exit": {1, errors.New("gone")},
	} {
		fs := &fakeShell{handler: func(string) (string, int, error) { return `{"A":"1"}`, reply.code, reply.err }}
		if got := readEnvCache(fs, ProbeLoginShell, "/tmp/sess"); got != nil {
			t.Errorf("%s: readEnvCache = %v, want a miss", name, got)
		}
	}
}

// parseEnvOutput reads one-letter names, appends a line without '=' to the
// value before it, and drops lines before the first variable.
func TestParseEnvOutputContinuationsAndShortNames(t *testing.T) {
	got := parseEnvOutput("stray line\nA=1\nB=x\ny\nC=3")
	want := map[string]string{"A": "1", "B": "x\ny", "C": "3"}
	if !maps.Equal(got, want) {
		t.Errorf("parseEnvOutput = %q, want %q", got, want)
	}
}
