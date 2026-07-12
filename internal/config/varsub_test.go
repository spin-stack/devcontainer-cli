package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVariableResolverBeforeContainerPhases(t *testing.T) {
	resolver := NewVariableResolver()
	ctx := SubstitutionContext{
		HostSubContext: HostSubContext{
			Platform:             "linux",
			LocalWorkspaceFolder: "/work/project",
			Env:                  map[string]string{"HOME": "/home/test"},
		},
		IDLabels: map[string]string{"devcontainer.local_folder": "/work/project"},
	}
	input := map[string]interface{}{
		"workspace": "${localWorkspaceFolder}",
		"id":        "${devcontainerId}",
		"later":     "${containerEnv:PATH}",
	}
	resolved, err := resolver.BeforeContainer(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	got := resolved.(map[string]interface{})
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

func TestVariableResolverEnvironmentErrorIncludesConfig(t *testing.T) {
	resolver := NewVariableResolver()
	_, err := resolver.Resolve(SubstitutionContext{HostSubContext: HostSubContext{
		ConfigFilePath: "/work/.devcontainer/devcontainer.json",
	}}, PhaseHost, "${localEnv}")
	if err == nil {
		t.Fatal("expected missing environment variable name to fail")
	}
	if !strings.Contains(err.Error(), "devcontainer.json") {
		t.Errorf("error does not identify config file: %v", err)
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

func TestVariableResolverHostPhase(t *testing.T) {
	localEnvCtx := HostSubContext{
		Platform: "linux",
		Env:      map[string]string{"HOME": "/home/user", "USER": "test"},
	}
	workspaceCtx := HostSubContext{
		Platform:                 "linux",
		LocalWorkspaceFolder:     "/home/user/project",
		ContainerWorkspaceFolder: "/workspaces/project",
		Env:                      map[string]string{},
	}
	emptyCtx := HostSubContext{Platform: "linux", Env: map[string]string{}}
	recursiveCtx := HostSubContext{
		Platform:             "linux",
		LocalWorkspaceFolder: "/home/user/project",
		Env:                  map[string]string{"HOME": "/home/user"},
	}

	tests := []struct {
		name  string
		ctx   HostSubContext
		input interface{}
		want  interface{}
	}{
		// localEnv substitution
		{"localEnv HOME", localEnvCtx, "${localEnv:HOME}", "/home/user"},
		{"env alias", localEnvCtx, "${env:HOME}", "/home/user"},
		{"localEnv with suffix", localEnvCtx, "${localEnv:HOME}/project", "/home/user/project"},
		{"localEnv missing", localEnvCtx, "${localEnv:MISSING}", ""},
		{"localEnv missing with fallback", localEnvCtx, "${localEnv:MISSING:fallback}", "fallback"},
		{"no vars", localEnvCtx, "no vars here", "no vars here"},
		{"multiple localEnv", localEnvCtx, "${localEnv:USER}@${localEnv:HOME}", "test@/home/user"},

		// workspace folder substitution
		{"localWorkspaceFolder", workspaceCtx, "${localWorkspaceFolder}", "/home/user/project"},
		{"localWorkspaceFolderBasename", workspaceCtx, "${localWorkspaceFolderBasename}", "project"},
		{"containerWorkspaceFolder", workspaceCtx, "${containerWorkspaceFolder}", "/workspaces/project"},
		{"containerWorkspaceFolderBasename", workspaceCtx, "${containerWorkspaceFolderBasename}", "project"},

		// unknown variables pass through unchanged
		{"unknown variable", emptyCtx, "${unknownVar}", "${unknownVar}"},

		// non-string values pass through unchanged
		{"number passthrough", emptyCtx, 42.0, 42.0},
		{"bool passthrough", emptyCtx, true, true},
		{"nil passthrough", emptyCtx, nil, nil},

		// recursive substitution into nested maps and slices
		{
			"recursive map and slice",
			recursiveCtx,
			map[string]interface{}{
				"workspaceFolder": "${localWorkspaceFolder}",
				"env": map[string]interface{}{
					"HOME": "${localEnv:HOME}",
				},
				"ports": []interface{}{"${localEnv:HOME}:8080"},
			},
			map[string]interface{}{
				"workspaceFolder": "/home/user/project",
				"env": map[string]interface{}{
					"HOME": "/home/user",
				},
				"ports": []interface{}{"/home/user:8080"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewVariableResolver().Resolve(SubstitutionContext{HostSubContext: tt.ctx}, PhaseHost, tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("host Resolve(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestVariableResolverIdentityPhase(t *testing.T) {
	labels := map[string]string{
		"devcontainer.local_folder": "/home/user/project",
	}
	id := ComputeDevContainerID(labels)
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	if len(id) != 52 {
		t.Errorf("id length = %d, want 52", len(id))
	}

	// Deterministic
	id2 := ComputeDevContainerID(labels)
	if id != id2 {
		t.Errorf("id not deterministic: %q != %q", id, id2)
	}

	// Substitute into value
	result, err := NewVariableResolver().Resolve(SubstitutionContext{IDLabels: labels}, PhaseIdentity, "${devcontainerId}")
	if err != nil {
		t.Fatal(err)
	}
	if result != id {
		t.Errorf("substitution = %q, want %q", result, id)
	}
}

func TestComputeDevContainerIDSortedKeys(t *testing.T) {
	labels1 := map[string]string{"a": "1", "b": "2"}
	labels2 := map[string]string{"b": "2", "a": "1"}

	id1 := ComputeDevContainerID(labels1)
	id2 := ComputeDevContainerID(labels2)

	if id1 != id2 {
		t.Errorf("key order should not matter: %q != %q", id1, id2)
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

func TestVariableResolverContainerPhase(t *testing.T) {
	containerEnv := map[string]string{
		"PATH":  "/usr/bin:/usr/local/bin",
		"SHELL": "/bin/bash",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${containerEnv:PATH}", "/usr/bin:/usr/local/bin"},
		{"${containerEnv:SHELL}", "/bin/bash"},
		{"${containerEnv:MISSING}", ""},
		{"no vars", "no vars"},
		// Non-container vars should pass through
		{"${localEnv:HOME}", "${localEnv:HOME}"},
	}

	for _, tt := range tests {
		got, err := NewVariableResolver().AfterContainer(SubstitutionContext{
			HostSubContext: HostSubContext{Platform: "linux"},
			ContainerEnv:   containerEnv,
		}, tt.input)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("container Resolve(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
