package imagemeta

import "testing"

func specBool(b bool) *bool    { return &b }
func specStr(s string) *string { return &s }

// TestSpecMetadataMergeRules locks the dev container specification's normative
// metadata merge rules (containers.dev/implementors/spec, "Merge logic"):
// entries are merged in order with devcontainer.json considered LAST, and each
// property category merges differently. This asserts the whole contract in one
// place, independent of the TS oracle.
func TestSpecMetadataMergeRules(t *testing.T) {
	// A feature entry (earlier) and the devcontainer.json entry (last).
	feature := Entry{
		Init:            specBool(true),
		Privileged:      specBool(false),
		RemoteUser:      "node",
		WaitFor:         "onCreateCommand",
		CapAdd:          []string{"SYS_PTRACE"},
		RemoteEnv:       map[string]*string{"FOO": specStr("from-feature")},
		OnCreateCommand: "feature-oncreate",
	}
	devcontainer := Entry{
		Init:            specBool(false), // spec: booleans are OR — must NOT override the feature's true
		Privileged:      specBool(true),
		RemoteUser:      "vscode", // spec: scalars — last (devcontainer.json) wins
		WaitFor:         "postCreateCommand",
		CapAdd:          []string{"SYS_ADMIN", "SYS_PTRACE"}, // spec: arrays — union, no duplicates
		RemoteEnv:       map[string]*string{"FOO": specStr("from-config"), "BAR": specStr("c")},
		OnCreateCommand: "config-oncreate",
	}

	m := MergeConfiguration([]Entry{feature, devcontainer})

	// Rule: boolean properties (init, privileged) are true if ANY source is true.
	if m.Init == nil || !*m.Init {
		t.Errorf("init = %v, want true (OR across sources: a feature requested it)", m.Init)
	}
	if m.Privileged == nil || !*m.Privileged {
		t.Errorf("privileged = %v, want true (OR across sources)", m.Privileged)
	}

	// Rule: scalar properties — the last source (devcontainer.json) wins.
	if m.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q, want vscode (last wins)", m.RemoteUser)
	}
	if m.WaitFor != "postCreateCommand" {
		t.Errorf("waitFor = %q, want postCreateCommand (last wins)", m.WaitFor)
	}

	// Rule: array properties — union without duplicates.
	if len(m.CapAdd) != 2 || !contains(m.CapAdd, "SYS_PTRACE") || !contains(m.CapAdd, "SYS_ADMIN") {
		t.Errorf("capAdd = %v, want a 2-element union {SYS_PTRACE, SYS_ADMIN}", m.CapAdd)
	}

	// Rule: object properties (remoteEnv) — merged per key, last value wins.
	if v := m.RemoteEnv["FOO"]; v == nil || *v != "from-config" {
		t.Errorf("remoteEnv[FOO] = %v, want from-config (last wins per key)", v)
	}
	if v := m.RemoteEnv["BAR"]; v == nil || *v != "c" {
		t.Errorf("remoteEnv[BAR] = %v, want c (union of keys)", v)
	}

	// Rule: lifecycle commands accumulate across sources, in entry order, so every
	// source's hook runs (devcontainer.json last).
	if len(m.OnCreateCommands) != 2 || m.OnCreateCommands[0] != "feature-oncreate" || m.OnCreateCommands[1] != "config-oncreate" {
		t.Errorf("onCreateCommands = %v, want [feature-oncreate config-oncreate]", m.OnCreateCommands)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
