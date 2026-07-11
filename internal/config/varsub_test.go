package config

import (
	"testing"
)

func TestSubstituteHost_LocalEnv(t *testing.T) {
	ctx := HostSubContext{
		Platform: "linux",
		Env:      map[string]string{"HOME": "/home/user", "USER": "test"},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${localEnv:HOME}", "/home/user"},
		{"${env:HOME}", "/home/user"},
		{"${localEnv:HOME}/project", "/home/user/project"},
		{"${localEnv:MISSING}", ""},
		{"${localEnv:MISSING:fallback}", "fallback"},
		{"no vars here", "no vars here"},
		{"${localEnv:USER}@${localEnv:HOME}", "test@/home/user"},
	}

	for _, tt := range tests {
		got := SubstituteHost(ctx, tt.input)
		if got != tt.want {
			t.Errorf("SubstituteHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSubstituteHost_WorkspaceFolder(t *testing.T) {
	ctx := HostSubContext{
		Platform:                 "linux",
		LocalWorkspaceFolder:     "/home/user/project",
		ContainerWorkspaceFolder: "/workspaces/project",
		Env:                      map[string]string{},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${localWorkspaceFolder}", "/home/user/project"},
		{"${localWorkspaceFolderBasename}", "project"},
		{"${containerWorkspaceFolder}", "/workspaces/project"},
		{"${containerWorkspaceFolderBasename}", "project"},
	}

	for _, tt := range tests {
		got := SubstituteHost(ctx, tt.input)
		if got != tt.want {
			t.Errorf("SubstituteHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSubstituteHost_UnknownVariable(t *testing.T) {
	ctx := HostSubContext{Platform: "linux", Env: map[string]string{}}
	got := SubstituteHost(ctx, "${unknownVar}")
	if got != "${unknownVar}" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestSubstituteHost_Recursive(t *testing.T) {
	ctx := HostSubContext{
		Platform:             "linux",
		LocalWorkspaceFolder: "/home/user/project",
		Env:                  map[string]string{"HOME": "/home/user"},
	}

	// Test map substitution
	input := map[string]interface{}{
		"workspaceFolder": "${localWorkspaceFolder}",
		"env": map[string]interface{}{
			"HOME": "${localEnv:HOME}",
		},
		"ports": []interface{}{"${localEnv:HOME}:8080"},
	}

	result := SubstituteHost(ctx, input)
	m := result.(map[string]interface{})
	if m["workspaceFolder"] != "/home/user/project" {
		t.Errorf("workspaceFolder = %v", m["workspaceFolder"])
	}
	envMap := m["env"].(map[string]interface{})
	if envMap["HOME"] != "/home/user" {
		t.Errorf("env.HOME = %v", envMap["HOME"])
	}
	ports := m["ports"].([]interface{})
	if ports[0] != "/home/user:8080" {
		t.Errorf("ports[0] = %v", ports[0])
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

func TestSubstituteHost_NonStringValues(t *testing.T) {
	ctx := HostSubContext{Platform: "linux", Env: map[string]string{}}

	// Numbers, bools, nil should pass through unchanged
	if got := SubstituteHost(ctx, 42.0); got != 42.0 {
		t.Errorf("number = %v", got)
	}
	if got := SubstituteHost(ctx, true); got != true {
		t.Errorf("bool = %v", got)
	}
	if got := SubstituteHost(ctx, nil); got != nil {
		t.Errorf("nil = %v", got)
	}
}
