package config

import (
	"reflect"
	"testing"
)

func TestSubstituteHost(t *testing.T) {
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
			got := SubstituteHost(tt.ctx, tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SubstituteHost(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSubstituteDevContainerID(t *testing.T) {
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
	result := SubstituteDevContainerID(labels, "${devcontainerId}")
	if result != id {
		t.Errorf("substitution = %q, want %q", result, id)
	}
}

func TestSubstituteDevContainerID_SortedKeys(t *testing.T) {
	labels1 := map[string]string{"a": "1", "b": "2"}
	labels2 := map[string]string{"b": "2", "a": "1"}

	id1 := ComputeDevContainerID(labels1)
	id2 := ComputeDevContainerID(labels2)

	if id1 != id2 {
		t.Errorf("key order should not matter: %q != %q", id1, id2)
	}
}

func TestSubstituteDevContainerIDString(t *testing.T) {
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
			if got := SubstituteDevContainerIDString(tt.id, tt.value); got != tt.want {
				t.Errorf("SubstituteDevContainerIDString(%q, %q) = %q, want %q", tt.id, tt.value, got, tt.want)
			}
		})
	}
}

func TestSubstituteTemplateOptions(t *testing.T) {
	options := map[string]string{"image": "ubuntu", "greeting": "hello"}
	value := "${templateOption:image}:${templateOption: greeting }:${unknown}"
	want := "ubuntu:hello:${unknown}"
	if got := SubstituteTemplateOptions(options, value); got != want {
		t.Errorf("SubstituteTemplateOptions() = %q, want %q", got, want)
	}
}

func TestSubstituteContainer(t *testing.T) {
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
		got := SubstituteContainer("linux", containerEnv, tt.input)
		if got != tt.want {
			t.Errorf("SubstituteContainer(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
