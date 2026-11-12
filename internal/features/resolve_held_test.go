package features

import "testing"

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
