package imagemeta

import (
	"reflect"
	"testing"
)

// Each test here encodes a merge rule from the TS mergeConfiguration
// (reference/src/spec-node/imageMetadata.ts) that the Go merge previously got
// wrong.

// TestMerge_MountsDedupByTarget: a later entry's mount to the same target must
// REPLACE the earlier one (TS mergeMounts de-dupes by target, keeping last).
func TestMerge_MountsDedupByTarget(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{Mounts: []interface{}{map[string]interface{}{"source": "vol-a", "target": "/data", "type": "volume"}}},
		{Mounts: []interface{}{map[string]interface{}{"source": "vol-b", "target": "/data", "type": "volume"}}},
	})
	if len(m.Mounts) != 1 {
		t.Fatalf("want 1 mount after target de-dup, got %d: %v", len(m.Mounts), m.Mounts)
	}
	if src := m.Mounts[0].(map[string]interface{})["source"]; src != "vol-b" {
		t.Errorf("kept the wrong mount: source=%v, want vol-b (last wins)", src)
	}
}

// TestMerge_EntrypointsNoDedup: identical entrypoints from two features must BOTH
// be kept (TS collectOrUndefined does not de-dupe; both scripts run).
func TestMerge_EntrypointsNoDedup(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{Entrypoint: "/init.sh"},
		{Entrypoint: "/init.sh"},
	})
	if len(m.Entrypoints) != 2 {
		t.Errorf("entrypoints = %v, want both kept (no de-dup)", m.Entrypoints)
	}
}

// TestMerge_ForwardPortsDedup: the number 3000 and duplicates collapse, treating
// N and "localhost:N" as the same port.
func TestMerge_ForwardPortsDedup(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{ForwardPorts: []interface{}{float64(3000)}},
		{ForwardPorts: []interface{}{float64(3000), float64(8080)}},
	})
	want := []interface{}{float64(3000), float64(8080)}
	if !reflect.DeepEqual(m.ForwardPorts, want) {
		t.Errorf("forwardPorts = %v, want %v", m.ForwardPorts, want)
	}

	// number 3000 and string "localhost:3000" are the same port -> one entry (a number).
	m2 := MergeConfiguration([]Entry{
		{ForwardPorts: []interface{}{float64(3000)}},
		{ForwardPorts: []interface{}{"localhost:3000"}},
	})
	if !reflect.DeepEqual(m2.ForwardPorts, []interface{}{float64(3000)}) {
		t.Errorf("forwardPorts = %v, want [3000]", m2.ForwardPorts)
	}

	// a host:port string is not a plain port and stays a string.
	m3 := MergeConfiguration([]Entry{{ForwardPorts: []interface{}{"db:5432", "db:5432"}}})
	if !reflect.DeepEqual(m3.ForwardPorts, []interface{}{"db:5432"}) {
		t.Errorf("forwardPorts = %v, want [db:5432]", m3.ForwardPorts)
	}
}

// TestMerge_HostRequirementsPerFieldMax: cpus from one entry and memory from
// another must both survive, with memory normalized to bytes (TS per-field max).
func TestMerge_HostRequirementsPerFieldMax(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{HostRequirements: map[string]interface{}{"cpus": float64(4)}},
		{HostRequirements: map[string]interface{}{"memory": "8gb"}},
		{HostRequirements: map[string]interface{}{"cpus": float64(2), "memory": "4gb"}},
	})
	hr, ok := m.HostRequirements.(map[string]interface{})
	if !ok {
		t.Fatalf("hostRequirements = %v (%T), want a map", m.HostRequirements, m.HostRequirements)
	}
	if hr["cpus"] != float64(4) {
		t.Errorf("cpus = %v, want 4 (max)", hr["cpus"])
	}
	if hr["memory"] != "8589934592" { // 8 * 2^30
		t.Errorf("memory = %v, want 8589934592 (max of 8gb/4gb, normalized)", hr["memory"])
	}

	// No entry sets hostRequirements -> nil.
	if got := MergeConfiguration([]Entry{{}}).HostRequirements; got != nil {
		t.Errorf("hostRequirements = %v, want nil", got)
	}
}

// TestMerge_LifecycleSkipsEmptyString: an empty-string command is not collected.
func TestMerge_LifecycleSkipsEmptyString(t *testing.T) {
	m := MergeConfiguration([]Entry{
		{OnCreateCommand: ""},
		{OnCreateCommand: "echo hi"},
	})
	if len(m.OnCreateCommands) != 1 || m.OnCreateCommands[0] != "echo hi" {
		t.Errorf("onCreateCommands = %v, want [echo hi] (empty string skipped)", m.OnCreateCommands)
	}
}
