package docker

// Oracle test ported VERBATIM from the upstream devcontainers CLI:
//   reference/src/test/utils.test.ts  (describe 'isBuildxCacheToInline').
// The cases (whitespace, casing, embedded in a larger spec) come from the
// reference author, so they pin our regex to the spec behavior.

import "testing"

func TestOracle_IsBuildxCacheToInline(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false}, // undefined/empty
		{"type=inline", true},
		{"type = inline", true},
		{"type=INLINE", true},
		{"mode=max,type=inline,compression=zstd", true},
		{"type=registry", false},
		{"type=local", false},
		{"inline", false},
	}
	for _, c := range cases {
		if got := isBuildxCacheToInline(c.in); got != c.want {
			t.Errorf("isBuildxCacheToInline(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
