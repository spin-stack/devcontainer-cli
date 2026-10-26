package features

import (
	"encoding/json"
	"reflect"
	"testing"
)

// DependsOnKeys is TS Object.keys(dependsOn): the metadata's order, a
// repeated key where it first appears, and keys set in code (not in the
// metadata) after them, sorted.
func TestDependsOnKeysKeepsTheMetadataOrder(t *testing.T) {
	var f Feature
	if err := json.Unmarshal([]byte(`{"id":"x","dependsOn":{"b":{},"a":{},"b":{"v":1}}}`), &f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.DependsOnKeys(), []string{"b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys = %v, want %v", got, want)
	}

	f = Feature{DependsOn: map[string]any{"c": nil, "z": nil, "a": nil}, DependsOnOrder: []string{"gone", "c"}}
	if got, want := f.DependsOnKeys(), []string{"c", "a", "z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys = %v, want %v", got, want)
	}
}

// A Feature with SourceKeys is written as TS's {...featureJson, ...feature}:
// the metadata's properties in its order (a repeated one where it first
// appears), then the source's that it lacks, then any other; without
// SourceKeys it keeps the struct's order.
func TestFeatureMarshalJSONKeyOrder(t *testing.T) {
	var f Feature
	meta := `{"version":"1.0.0","containerEnv":{"K":"v"},"id":"x","options":{},"version":"1.0.1"}`
	if err := json.Unmarshal([]byte(meta), &f); err != nil {
		t.Fatal(err)
	}
	f.Name, f.Value, f.Included, f.CachePath, f.ConsecutiveId, f.CapAdd = "./x", true, true, "/c", "x_0", []string{"SYS_PTRACE"}
	for _, tc := range []struct {
		source []string
		want   string
	}{
		{LocalFeatureKeys, `{"version":"1.0.1","containerEnv":{"K":"v"},"id":"x","name":"./x","value":true,"included":true,"cachePath":"/c","consecutiveId":"x_0","capAdd":["SYS_PTRACE"]}`},
		{OCIFeatureKeys, `{"version":"1.0.1","containerEnv":{"K":"v"},"id":"x","included":true,"value":true,"cachePath":"/c","consecutiveId":"x_0","name":"./x","capAdd":["SYS_PTRACE"]}`},
		{nil, `{"id":"x","version":"1.0.1","name":"./x","included":true,"cachePath":"/c","consecutiveId":"x_0","value":true,"containerEnv":{"K":"v"},"capAdd":["SYS_PTRACE"]}`},
	} {
		f.SourceKeys = tc.source
		got, err := json.Marshal(f)
		if err != nil || string(got) != tc.want {
			t.Errorf("SourceKeys %v: %s, %v; want %s", tc.source, got, err, tc.want)
		}
	}
}
