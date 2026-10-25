package docker

import (
	"errors"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// Down reports a compose process that could not run and one that exited
// non-zero, and succeeds only on exit 0.
func TestDownFailsUnlessComposeExitsZero(t *testing.T) {
	newClient := func(r *fakeRunner) *ComposeClient {
		return &ComposeClient{Command: []string{"docker", "compose"}, Log: log.Null, Runner: r}
	}

	ok := &fakeRunner{}
	if err := newClient(ok).Down(t.Context(), []string{"a.yml"}, "proj", true); err != nil {
		t.Fatalf("Down = %v, want nil", err)
	}
	if want := []string{"compose", "-f", "a.yml", "--project-name", "proj", "down", "--volumes"}; !slices.Equal(ok.gotArgs, want) {
		t.Errorf("args = %q, want %q", ok.gotArgs, want)
	}

	sentinel := errors.New("exec: docker: not found")
	if err := newClient(&fakeRunner{err: sentinel}).Down(t.Context(), nil, "", false); !errors.Is(err, sentinel) {
		t.Errorf("Down with a failed run = %v, want it to wrap %v", err, sentinel)
	}
	if err := newClient(&fakeRunner{code: 1, stderr: []byte("boom")}).Down(t.Context(), nil, "", false); err == nil {
		t.Error("Down with exit 1 = nil, want an error")
	}
}

// TS parseVersion (spec-common/commonUtils.ts) keeps every numeric component.
func TestParseComposeVersionKeepsEveryComponent(t *testing.T) {
	if got, want := parseComposeVersion("2.24.5.1"), []int{2, 24, 5, 1}; !slices.Equal(got, want) {
		t.Errorf("parseComposeVersion = %v, want %v", got, want)
	}
}

// TS compares the whole version with isEarlierVersion, a missing component
// counting as 0: useNewProjectName (dockerCompose.ts) is
// !isEarlierVersion(version, [1, 21, 0]), optimistic when the version is
// unknown, and additional build contexts need !isEarlierVersion(version,
// [2, 17, 0]) (buildAndExtendDockerCompose). A one-component version is
// therefore 1.0.0, 2.0.0 or 3.0.0, not "unknown".
func TestComposeVersionGatesCountAMissingComponentAsZero(t *testing.T) {
	for _, tc := range []struct {
		version                       []int
		newProjectNames, addlContexts bool
	}{
		{nil, true, false},
		{[]int{1}, false, false},
		{[]int{1, 20}, false, false},
		{[]int{1, 21}, true, false},
		{[]int{2}, true, false},
		{[]int{2, 16, 9}, true, false},
		{[]int{2, 17}, true, true},
		{[]int{3}, true, true},
	} {
		c := &ComposeClient{Version: tc.version}
		if got := c.UsesNewProjectNames(); got != tc.newProjectNames {
			t.Errorf("UsesNewProjectNames(%v) = %v, want %v", tc.version, got, tc.newProjectNames)
		}
		if got := c.SupportsAdditionalContexts(); got != tc.addlContexts {
			t.Errorf("SupportsAdditionalContexts(%v) = %v, want %v", tc.version, got, tc.addlContexts)
		}
	}
}
