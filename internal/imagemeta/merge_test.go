package imagemeta

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/core/log"
)

func TestMergeConfiguration_Scalars_LastWins(t *testing.T) {
	entries := []Entry{
		{RemoteUser: "base-user", ContainerUser: "base-container", ShutdownAction: "stopContainer"},
		{RemoteUser: "feature-user"},
		{RemoteUser: "config-user", UserEnvProbe: "loginInteractiveShell", RunArgs: []string{"-e", "A=B"}},
	}
	m := MergeConfiguration(entries)

	if m.RemoteUser != "config-user" {
		t.Errorf("remoteUser = %q, want 'config-user' (last wins)", m.RemoteUser)
	}
	if m.ContainerUser != "base-container" {
		t.Errorf("containerUser = %q, want 'base-container' (only set in first)", m.ContainerUser)
	}
	if m.UserEnvProbe != "loginInteractiveShell" {
		t.Errorf("userEnvProbe = %q", m.UserEnvProbe)
	}
	if m.ShutdownAction != "stopContainer" {
		t.Errorf("shutdownAction = %q, want 'stopContainer'", m.ShutdownAction)
	}
	if len(m.RunArgs) != 2 || m.RunArgs[0] != "-e" || m.RunArgs[1] != "A=B" {
		t.Errorf("runArgs = %#v", m.RunArgs)
	}
}

func TestMergeConfiguration_LifecycleHooks_Concatenated(t *testing.T) {
	entries := []Entry{
		{OnCreateCommand: "echo base", PostCreateCommand: "setup-base"},
		{OnCreateCommand: "echo feature", PostStartCommand: "start-feature"},
		{OnCreateCommand: "echo config", PostCreateCommand: "setup-config"},
	}
	m := MergeConfiguration(entries)

	if len(m.OnCreateCommands) != 3 {
		t.Errorf("onCreateCommands len = %d, want 3", len(m.OnCreateCommands))
	}
	if len(m.PostCreateCommands) != 2 {
		t.Errorf("postCreateCommands len = %d, want 2", len(m.PostCreateCommands))
	}
	if len(m.PostStartCommands) != 1 {
		t.Errorf("postStartCommands len = %d, want 1", len(m.PostStartCommands))
	}
}

func TestMergeConfiguration_Arrays_Union(t *testing.T) {
	entries := []Entry{
		{CapAdd: []string{"SYS_PTRACE"}},
		{CapAdd: []string{"SYS_PTRACE", "NET_ADMIN"}},
		{SecurityOpt: []string{"seccomp=unconfined"}},
	}
	m := MergeConfiguration(entries)

	if len(m.CapAdd) != 2 {
		t.Errorf("capAdd len = %d, want 2 (deduplicated)", len(m.CapAdd))
	}
	if len(m.SecurityOpt) != 1 {
		t.Errorf("securityOpt len = %d", len(m.SecurityOpt))
	}
}

func TestMergeConfiguration_Maps_Merged(t *testing.T) {
	entries := []Entry{
		{ContainerEnv: map[string]string{"A": "1", "B": "2"}},
		{ContainerEnv: map[string]string{"B": "overridden", "C": "3"}},
	}
	m := MergeConfiguration(entries)

	if m.ContainerEnv["A"] != "1" {
		t.Errorf("A = %q", m.ContainerEnv["A"])
	}
	if m.ContainerEnv["B"] != "overridden" {
		t.Errorf("B = %q, want 'overridden'", m.ContainerEnv["B"])
	}
	if m.ContainerEnv["C"] != "3" {
		t.Errorf("C = %q", m.ContainerEnv["C"])
	}
}

func TestMergeConfiguration_Empty(t *testing.T) {
	m := MergeConfiguration(nil)
	if m.RemoteUser != "" {
		t.Errorf("remoteUser = %q, want empty", m.RemoteUser)
	}
	if len(m.OnCreateCommands) != 0 {
		t.Errorf("onCreateCommands should be empty")
	}
}

func TestMergeConfiguration_BoolPointers(t *testing.T) {
	trueVal := true
	falseVal := false
	entries := []Entry{
		{Init: &trueVal},
		{Privileged: &falseVal},
	}
	m := MergeConfiguration(entries)

	if m.Init == nil || !*m.Init {
		t.Error("init should be true")
	}
	if m.Privileged == nil || *m.Privileged {
		t.Error("privileged should be false")
	}
}

func TestMergeConfiguration_InitPrivileged_OR(t *testing.T) {
	trueVal := true
	falseVal := false
	// A later entry with init:false must NOT override an earlier init:true (OR).
	entries := []Entry{
		{Init: &trueVal, Privileged: &falseVal},
		{Init: &falseVal, Privileged: &trueVal},
	}
	m := MergeConfiguration(entries)
	if m.Init == nil || !*m.Init {
		t.Error("init should be true (OR across entries)")
	}
	if m.Privileged == nil || !*m.Privileged {
		t.Error("privileged should be true (OR across entries)")
	}
}

func TestMergeConfiguration_Customizations_ArraysByKey(t *testing.T) {
	// Each entry's customizations[key] is collected into an array, matching TS,
	// so no feature's settings/extensions are clobbered.
	entries := []Entry{
		{Customizations: map[string]interface{}{"vscode": map[string]interface{}{"extensions": []interface{}{"a"}}}},
		{Customizations: map[string]interface{}{"vscode": map[string]interface{}{"extensions": []interface{}{"b"}}}},
	}
	m := MergeConfiguration(entries)
	vscode := m.Customizations["vscode"]
	if len(vscode) != 2 {
		t.Fatalf("customizations.vscode should have 2 entries, got %d", len(vscode))
	}
}

func TestReadMetadataFromLabels(t *testing.T) {
	entries := []Entry{
		{ID: "testid", RemoteUser: "vscode"},
	}
	data, _ := json.Marshal(entries)
	labels := map[string]string{
		MetadataLabel: string(data),
	}

	result := ReadMetadataFromLabels(labels, log.Null)
	if len(result) != 1 {
		t.Fatalf("entries = %d", len(result))
	}
	if result[0].ID != "testid" {
		t.Errorf("id = %q", result[0].ID)
	}
	if result[0].RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q", result[0].RemoteUser)
	}
}

func TestReadMetadataFromLabels_Missing(t *testing.T) {
	result := ReadMetadataFromLabels(map[string]string{}, log.Null)
	if result != nil {
		t.Error("expected nil for missing label")
	}
}

func TestReadMetadataFromLabels_Invalid(t *testing.T) {
	labels := map[string]string{MetadataLabel: "not json"}
	result := ReadMetadataFromLabels(labels, log.Null)
	if result != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestGenerateMetadataLabel_Roundtrip(t *testing.T) {
	entries := []Entry{
		{ID: "go", RemoteUser: "vscode"},
		{ID: "node", ContainerEnv: map[string]string{"NODE_ENV": "dev"}},
	}
	label := GenerateMetadataLabel(entries)

	var parsed []Entry
	if err := json.Unmarshal([]byte(label), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("parsed = %d", len(parsed))
	}
	if parsed[0].ID != "go" || parsed[1].ID != "node" {
		t.Errorf("roundtrip failed: %v", parsed)
	}
}
