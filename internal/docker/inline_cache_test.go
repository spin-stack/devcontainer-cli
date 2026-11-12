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

	withReg := join(c.buildArgs(BuildOptions{UseBuildx: true, CacheTo: "type=registry,ref=r/c", ContextPath: "."}))
	if !strings.Contains(withReg, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("expected inline-cache build-arg for registry cache-to: %s", withReg)
	}
	withInline := join(c.buildArgs(BuildOptions{UseBuildx: true, CacheTo: "type=inline", ContextPath: "."}))
	if strings.Contains(withInline, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("inline-cache build-arg must be omitted for inline cache-to: %s", withInline)
	}
	none := join(c.buildArgs(BuildOptions{UseBuildx: true, ContextPath: "."}))
	if !strings.Contains(none, "BUILDKIT_INLINE_CACHE=1") {
		t.Errorf("expected inline-cache build-arg when no cache-to: %s", none)
	}
}

func TestBuildArgsSecrets(t *testing.T) {
	c := &Client{DockerPath: "docker", Log: log.Null}
	args := c.buildArgs(BuildOptions{UseBuildx: true, ContextPath: ".", Secrets: []string{"TOKEN=abc", "NPM=xyz", "BAD"}})
	s := strings.Join(args, " ")
	if !strings.Contains(s, "--secret id=TOKEN,env=TOKEN") || !strings.Contains(s, "--secret id=NPM,env=NPM") {
		t.Fatalf("missing --secret refs: %s", s)
	}
	if strings.Contains(s, "abc") || strings.Contains(s, "xyz") {
		t.Fatalf("secret value leaked into args: %s", s)
	}
	if strings.Contains(s, "id=BAD") {
		t.Fatalf("malformed secret should be skipped: %s", s)
	}
	legacy := strings.Join(c.buildArgs(BuildOptions{UseBuildx: false, ContextPath: ".", Secrets: []string{"TOKEN=abc"}}), " ")
	if strings.Contains(legacy, "--secret") {
		t.Fatalf("legacy build must not emit --secret: %s", legacy)
	}
}

func TestBuildArgsFileSecrets(t *testing.T) {
	c := &Client{DockerPath: "docker", Log: log.Null}
	files := map[string]string{"b-id": "/x/b.pem", "a-id": "/x/a.pem"}
	s := strings.Join(c.buildArgs(BuildOptions{UseBuildx: true, ContextPath: ".", FileSecrets: files}), " ")
	if want := "--secret id=a-id,src=/x/a.pem --secret id=b-id,src=/x/b.pem"; !strings.Contains(s, want) {
		t.Fatalf("args = %s, want %q", s, want)
	}
	legacy := strings.Join(c.buildArgs(BuildOptions{ContextPath: ".", FileSecrets: files}), " ")
	if strings.Contains(legacy, "--secret") {
		t.Fatalf("legacy build must not emit --secret: %s", legacy)
	}
}

func TestBuildSecretsRouteThroughEnv(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("ok"), code: 0}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}
	if _, err := c.Build(t.Context(), BuildOptions{UseBuildx: true, ContextPath: ".", Secrets: []string{"TOKEN=abc"}}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range fr.gotArgs {
		if strings.Contains(a, "abc") {
			t.Fatalf("secret value leaked into args: %v", fr.gotArgs)
		}
	}
}
