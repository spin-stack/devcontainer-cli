package imagemeta

import (
	"reflect"
	"testing"
)

func TestMerge_MountsDedupByTarget(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{Mounts: []any{map[string]any{"source": "vol-a", "target": "/data", "type": "volume"}}},
		{Mounts: []any{map[string]any{"source": "vol-b", "target": "/data", "type": "volume"}}},
	})
	if len(m.Mounts) != 1 {
		t.Fatalf("want 1 mount after target de-dup, got %d: %v", len(m.Mounts), m.Mounts)
	}
	if src := m.Mounts[0].(map[string]any)["source"]; src != "vol-b" {
		t.Errorf("kept the wrong mount: source=%v, want vol-b (last wins)", src)
	}
}

func TestMerge_EntrypointsNoDedup(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{Entrypoint: "/init.sh"},
		{Entrypoint: "/init.sh"},
	})
	if len(m.Entrypoints) != 2 {
		t.Errorf("entrypoints = %v, want both kept (no de-dup)", m.Entrypoints)
	}
}

func TestMerge_ForwardPortsDedup(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{ForwardPorts: []any{float64(3000)}},
		{ForwardPorts: []any{float64(3000), float64(8080)}},
	})
	want := []any{float64(3000), float64(8080)}
	if !reflect.DeepEqual(m.ForwardPorts, want) {
		t.Errorf("forwardPorts = %v, want %v", m.ForwardPorts, want)
	}

	m2 := MergeConfiguration([]Entry{
		{ForwardPorts: []any{float64(3000)}},
		{ForwardPorts: []any{"localhost:3000"}},
	})
	if !reflect.DeepEqual(m2.ForwardPorts, []any{float64(3000)}) {
		t.Errorf("forwardPorts = %v, want [3000]", m2.ForwardPorts)
	}

	m3 := MergeConfiguration([]Entry{{ForwardPorts: []any{"db:5432", "db:5432"}}})
	if !reflect.DeepEqual(m3.ForwardPorts, []any{"db:5432"}) {
		t.Errorf("forwardPorts = %v, want [db:5432]", m3.ForwardPorts)
	}
}

func TestMerge_HostRequirementsPerFieldMax(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{HostRequirements: map[string]any{"cpus": float64(4)}},
		{HostRequirements: map[string]any{"memory": "8gb"}},
		{HostRequirements: map[string]any{"cpus": float64(2), "memory": "4gb"}},
	})
	hr, ok := m.HostRequirements.(map[string]any)
	if !ok {
		t.Fatalf("hostRequirements = %v (%T), want a map", m.HostRequirements, m.HostRequirements)
	}
	if hr["cpus"] != float64(4) {
		t.Errorf("cpus = %v, want 4 (max)", hr["cpus"])
	}
	if hr["memory"] != "8589934592" {
		t.Errorf("memory = %v, want 8589934592 (max of 8gb/4gb, normalized)", hr["memory"])
	}

	if got := MergeConfiguration([]Entry{{RemoteUser: "x"}}).HostRequirements; got != nil {
		t.Errorf("hostRequirements = %v, want nil", got)
	}
	want := map[string]any{"cpus": nil, "memory": "-Infinity", "storage": "-Infinity"}
	if got := MergeConfiguration([]Entry{{}}).HostRequirements; !reflect.DeepEqual(got, want) {
		t.Errorf("hostRequirements = %v, want %v", got, want)
	}
	if got := GenerateMetadataLabel([]Entry{{}, {RemoteUser: "x"}}); got != `[{"remoteUser":"x"}]` {
		t.Errorf("label = %s, want the empty entry dropped", got)
	}
}

func TestMerge_LifecycleSkipsEmptyString(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{OnCreateCommand: ""},
		{OnCreateCommand: "echo hi"},
	})
	if len(m.OnCreateCommands) != 1 || m.OnCreateCommands[0] != "echo hi" {
		t.Errorf("onCreateCommands = %v, want [echo hi] (empty string skipped)", m.OnCreateCommands)
	}
}
