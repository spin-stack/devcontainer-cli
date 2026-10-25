package config

import (
	"errors"
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

func TestSubstituteTemplateOptions(t *testing.T) {
	options := map[string]string{"image": "ubuntu", "greeting": "hello"}
	value := "${templateOption:image}:${templateOption: greeting }:${unknown}"
	want := "ubuntu:hello:${unknown}"
	got, err := NewVariableResolver().Resolve(SubstitutionContext{TemplateOptions: options}, PhaseTemplate, value)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("template Resolve() = %q, want %q", got, want)
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
