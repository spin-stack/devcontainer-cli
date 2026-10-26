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
