package imagemeta

// Oracle tests ported VERBATIM from the upstream devcontainers CLI test suite:
//   reference/src/test/imageMetadata.test.ts  (describe 'mergeConfiguration').
//
// The inputs and expected values come from the reference author (who defines the
// spec behavior), NOT from our reading of it — so these catch a divergence our
// own hand-written tests cannot, because our tests would encode the same
// misunderstanding as our code. Entries are built from the exact JSON the TS test
// uses, to avoid hand-translation drift.

import (
	"encoding/json"
	"testing"
)

func mustEntries(t *testing.T, jsonArray string) []Entry {
	t.Helper()
	var entries []Entry
	if err := json.Unmarshal([]byte(jsonArray), &entries); err != nil {
		t.Fatalf("bad entries JSON: %v", err)
	}
	return entries
}

// Ported from: it('should merge metadata from devcontainer.json and features').
// The config's remoteEnv is the LAST entry (our MergeConfiguration takes the
// metadata array; the caller appends the config entry, mirroring the TS
// imageMetadata array whose last element carries the config's remoteEnv).
func TestOracle_MergeRemoteEnv(t *testing.T) {
	merged := MergeConfiguration(mustEntries(t, `[
		{ "remoteEnv": { "ENV1": "feature1", "ENV2": "feature1", "ENV3": "feature1", "ENV4": "feature1" } },
		{ "remoteEnv": { "ENV1": "feature2", "ENV2": "feature2" } },
		{ "remoteEnv": { "ENV1": "devcontainer.json", "ENV3": "devcontainer.json" } }
	]`))
	want := map[string]string{
		"ENV1": "devcontainer.json",
		"ENV2": "feature2",
		"ENV3": "devcontainer.json",
		"ENV4": "feature1",
	}
	for k, v := range want {
		got := merged.RemoteEnv[k]
		if got == nil || *got != v {
			t.Errorf("remoteEnv[%s] = %v, want %q", k, got, v)
		}
	}
}

// Ported from: it('should deduplicate mounts'). Mixed string+object mounts across
// three entries; de-dup by target keeping the LAST, preserving order.
func TestOracle_DeduplicateMounts(t *testing.T) {
	merged := MergeConfiguration(mustEntries(t, `[
		{ "mounts": [
			"source=source1,dst=target1,type=volume",
			"source=source2,target=target2,type=volume",
			"source=source3,destination=target3,type=volume"
		] },
		{ "mounts": [ { "source": "source4", "target": "target1", "type": "volume" } ] },
		{ "mounts": [ { "source": "source5", "target": "target3", "type": "volume" } ] }
	]`))

	if len(merged.Mounts) != 3 {
		t.Fatalf("mounts length = %d, want 3: %v", len(merged.Mounts), merged.Mounts)
	}
	if s, ok := merged.Mounts[0].(string); !ok || s != "source=source2,target=target2,type=volume" {
		t.Errorf("mounts[0] = %v, want the source2 string mount", merged.Mounts[0])
	}
	if m, ok := merged.Mounts[1].(map[string]interface{}); !ok || m["source"] != "source4" {
		t.Errorf("mounts[1] = %v, want the source4 object", merged.Mounts[1])
	}
	if m, ok := merged.Mounts[2].(map[string]interface{}); !ok || m["source"] != "source5" {
		t.Errorf("mounts[2] = %v, want the source5 object", merged.Mounts[2])
	}
}

// Ported from: it('should merge gpu requirements from devcontainer.json and
// features'). The config's gpu:'optional' is NOT part of the merge (TS reduces
// over the metadata array only), so only the three feature entries are passed.
func TestOracle_MergeGPURequirements(t *testing.T) {
	merged := MergeConfiguration(mustEntries(t, `[
		{ "hostRequirements": { "gpu": true } },
		{ "hostRequirements": { "gpu": { "cores": 4 } } },
		{ "hostRequirements": { "gpu": { "memory": "8gb" } } }
	]`))

	hr, ok := merged.HostRequirements.(map[string]interface{})
	if !ok {
		t.Fatalf("hostRequirements = %v (%T), want a map", merged.HostRequirements, merged.HostRequirements)
	}
	gpu, ok := hr["gpu"].(map[string]interface{})
	if !ok {
		t.Fatalf("gpu = %v (%T), want a map", hr["gpu"], hr["gpu"])
	}
	if gpu["cores"] != float64(4) {
		t.Errorf("gpu.cores = %v, want 4", gpu["cores"])
	}
	if gpu["memory"] != "8589934592" {
		t.Errorf("gpu.memory = %v, want 8589934592", gpu["memory"])
	}
}
