package imagemeta

import "slices"

import "testing"

func TestSpecMetadataMergeRules(t *testing.T) {
	feature := Entry{
		Init:            new(true),
		Privileged:      new(false),
		RemoteUser:      "node",
		WaitFor:         "onCreateCommand",
		CapAdd:          []string{"SYS_PTRACE"},
		RemoteEnv:       map[string]*string{"FOO": new("from-feature")},
		OnCreateCommand: "feature-oncreate",
	}
	devcontainer := Entry{
		Init:            new(false),
		Privileged:      new(true),
		RemoteUser:      "vscode",
		WaitFor:         "postCreateCommand",
		CapAdd:          []string{"SYS_ADMIN", "SYS_PTRACE"},
		RemoteEnv:       map[string]*string{"FOO": new("from-config"), "BAR": new("c")},
		OnCreateCommand: "config-oncreate",
	}

	m := MergeConfiguration([]Entry{feature, devcontainer})

	if m.Init == nil || !*m.Init {
		t.Errorf("init = %v, want true (OR across sources: a feature requested it)", m.Init)
	}
	if m.Privileged == nil || !*m.Privileged {
		t.Errorf("privileged = %v, want true (OR across sources)", m.Privileged)
	}

	if m.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q, want vscode (last wins)", m.RemoteUser)
	}
	if m.WaitFor != "postCreateCommand" {
		t.Errorf("waitFor = %q, want postCreateCommand (last wins)", m.WaitFor)
	}

	if len(m.CapAdd) != 2 || !contains(m.CapAdd, "SYS_PTRACE") || !contains(m.CapAdd, "SYS_ADMIN") {
		t.Errorf("capAdd = %v, want a 2-element union {SYS_PTRACE, SYS_ADMIN}", m.CapAdd)
	}

	if v := m.RemoteEnv["FOO"]; v == nil || *v != "from-config" {
		t.Errorf("remoteEnv[FOO] = %v, want from-config (last wins per key)", v)
	}
	if v := m.RemoteEnv["BAR"]; v == nil || *v != "c" {
		t.Errorf("remoteEnv[BAR] = %v, want c (union of keys)", v)
	}

	if len(m.OnCreateCommands) != 2 || m.OnCreateCommands[0] != "feature-oncreate" || m.OnCreateCommands[1] != "config-oncreate" {
		t.Errorf("onCreateCommands = %v, want [feature-oncreate config-oncreate]", m.OnCreateCommands)
	}
}

func contains(xs []string, want string) bool {
	return slices.Contains(xs, want)
}
