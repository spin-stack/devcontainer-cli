package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestVariableResolverBeforeContainerPhases(t *testing.T) {
	resolver := NewVariableResolver()
	ctx := SubstitutionContext{
		Platform:             "linux",
		LocalWorkspaceFolder: "/work/project",
		Env:                  map[string]string{"HOME": "/home/test"},
		IDLabels:             map[string]string{"devcontainer.local_folder": "/work/project"},
	}
	input := map[string]any{
		"workspace": "${localWorkspaceFolder}",
		"id":        "${devcontainerId}",
		"later":     "${containerEnv:PATH}",
	}
	if err := resolver.BeforeContainerInto(ctx, &input); err != nil {
		t.Fatal(err)
	}
	got := input
	if got["workspace"] != "/work/project" {
		t.Errorf("workspace = %q", got["workspace"])
	}
	if got["id"] != ComputeDevContainerID(ctx.IDLabels) {
		t.Errorf("id = %q", got["id"])
	}
	if got["later"] != "${containerEnv:PATH}" {
		t.Errorf("container phase placeholder resolved too early: %q", got["later"])
	}
}

func TestVariableResolverResolveInto(t *testing.T) {
	type value struct {
		Env map[string]string `json:"env"`
	}
	target := value{Env: map[string]string{"ID": "prefix-${devcontainerId}"}}
	ctx := SubstitutionContext{DevContainerID: "abc123"}
	if err := NewVariableResolver().ResolveInto(ctx, PhaseIdentity, &target); err != nil {
		t.Fatal(err)
	}
	if target.Env["ID"] != "prefix-abc123" {
		t.Errorf("typed target was not resolved: %#v", target)
	}
}

func TestVariableResolverIdentityStrings(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		value string
		want  string
	}{
		{"multiple", "abc123", "cache-${devcontainerId}/${devcontainerId}", "cache-abc123/abc123"},
		{"unknown preserved", "abc123", "${unknown}:${devcontainerId}", "${unknown}:abc123"},
		{"similar name preserved", "abc123", "${devcontainerIdExtra}", "${devcontainerIdExtra}"},
		{"empty id preserved", "", "cache-${devcontainerId}", "cache-${devcontainerId}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewVariableResolver().Resolve(SubstitutionContext{DevContainerID: tt.id}, PhaseIdentity, tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("identity Resolve(%q, %q) = %q, want %q", tt.id, tt.value, got, tt.want)
			}
		})
	}
}

func TestSubstituteTags(t *testing.T) {
	upper := func(tag string) (string, bool, error) { return strings.ToUpper(tag), true, nil }

	// No tags: input returned unchanged (fast path).
	if got, _ := substituteTags("plain text", upper); got != "plain text" {
		t.Errorf("no-tag = %q", got)
	}
	// Multiple tags replaced.
	if got, _ := substituteTags("a ${one} b ${two}", upper); got != "a ONE b TWO" {
		t.Errorf("multi = %q", got)
	}
	// Unhandled tag kept verbatim so a later phase can resolve it.
	keepFoo := func(tag string) (string, bool, error) {
		if tag == "foo" {
			return "", false, nil
		}
		return "X", true, nil
	}
	if got, _ := substituteTags("${foo}:${bar}", keepFoo); got != "${foo}:X" {
		t.Errorf("verbatim = %q", got)
	}
	// Unterminated "${" is kept as-is.
	if got, _ := substituteTags("before ${tag", upper); got != "before ${tag" {
		t.Errorf("unterminated = %q", got)
	}
	// replace errors abort and propagate.
	boom := errors.New("boom")
	if _, err := substituteTags("${x}", func(string) (string, bool, error) { return "", false, boom }); !errors.Is(err, boom) {
		t.Errorf("error prop = %v", err)
	}
}

// The id is the hash in base 32 padded to 52 digits, also when the hash's
// leading digits are zeros.
func TestComputeDevContainerIDIsPaddedTo52Digits(t *testing.T) {
	padded := false
	for i := 0; i < 200; i++ {
		labels := map[string]string{"devcontainer.local_folder": fmt.Sprintf("/ws/%d", i)}
		id := ComputeDevContainerID(labels)
		if len(id) != 52 {
			t.Fatalf("id for %v = %q, %d digits", labels, id, len(id))
		}
		padded = padded || id[0] == '0'
	}
	if !padded {
		t.Error("no id needed padding: widen the search")
	}
}

// Only a letter followed by a colon is a drive (node path.win32.basename):
// '{' lowercases past 'z', so `{:foo` keeps its colon.
func TestWinBasenameDrive(t *testing.T) {
	for in, want := range map[string]string{`C:foo`: "foo", `c:\a\b\`: "b", `1:foo`: "1:foo", `C:`: "", `{:foo`: "{:foo", "@:foo": "@:foo"} {
		if got := winBasename(in); got != want {
			t.Errorf("winBasename(%q) = %q, want %q", in, got, want)
		}
	}
}
