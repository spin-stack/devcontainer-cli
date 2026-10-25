package features

import "testing"

// One-character names are names: "a/b" is a GitHub shorthand, resolved to
// ghcr.io/a/b like any owner/feature, and the explicit tag of the legacy
// shorthand "a:2" is its tag (TestResolveID has the longer names).
func TestResolveIDReadsOneCharacterNames(t *testing.T) {
	for _, tt := range []struct{ id, want string }{
		{"a/b", "ghcr.io/a/b"},
		{"a/b:2", "ghcr.io/a/b:2"},
		{"a:2", "ghcr.io/devcontainers/features/a:2"},
	} {
		if got, mapped := ResolveID(tt.id, false); got != tt.want || !mapped {
			t.Errorf("ResolveID(%q) = %q, %v; want %q, true", tt.id, got, mapped, tt.want)
		}
	}
}
