package docker

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestIsBuildxCacheToInline(t *testing.T) {
	cases := map[string]bool{
		"":                           false,
		"type=inline":                true,
		"type = inline":              true,
		"type=Inline":                true,
		"type=registry,ref=r/c:main": false,
		"type=local,dest=/tmp/c":     false,
		"mode=max,type=inline":       true,
	}
	for in, want := range cases {
		if got := isBuildxCacheToInline(in); got != want {
			t.Errorf("isBuildxCacheToInline(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildArgsInlineCacheGuard(t *testing.T) {
	c := &Client{DockerPath: "docker", Log: log.Null}
	join := func(a []string) string { return strings.Join(a, " ") }

	// Non-inline cache-to → inline-cache build-arg present.
	withReg := join(c.buildArgs(BuildOptions{UseBuildx: true, CacheTo: "type=registry,ref=r/c", ContextPath: "."}))
	if !strings.Contains(withReg, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("expected inline-cache build-arg for registry cache-to: %s", withReg)
	}
	// Inline cache-to → build-arg omitted (matches TS).
	withInline := join(c.buildArgs(BuildOptions{UseBuildx: true, CacheTo: "type=inline", ContextPath: "."}))
	if strings.Contains(withInline, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("inline-cache build-arg must be omitted for inline cache-to: %s", withInline)
	}
	// No cache-to → default keeps the inline-cache build-arg (unchanged behavior).
	none := join(c.buildArgs(BuildOptions{UseBuildx: true, ContextPath: "."}))
	if !strings.Contains(none, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("expected inline-cache build-arg when no cache-to: %s", none)
	}
}
