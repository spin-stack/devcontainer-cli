package oci

import (
	"strings"
	"testing"
)

// TS getRef (containerCollectionsOCI.ts) splits a digest on every colon and
// wants exactly two parts, then checks the algorithm is sha256, then checks the
// hex against regexForVersionOrDigest: each rejection names what failed.
func TestParseRefDigestRejections(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"ghcr.io/ns/id/feat@sha256:a:b", `invalid digest format "sha256:a:b"`},
		{"ghcr.io/ns/id/feat@md5:abc", `unsupported digest algorithm "md5"`},
		{"ghcr.io/ns/id/feat@sha256:-bad", `digest "-bad" does not match expected pattern`},
	} {
		if _, err := ParseRef(tt.input); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ParseRef(%q) error = %v, want %q", tt.input, err, tt.want)
		}
	}
}
