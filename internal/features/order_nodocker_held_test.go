package features

import (
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// optionsNode is one reference to ghcr.io/ns/a with the given options; label
// tells the nodes apart in the install order.
func optionsNode(label string, options any) *FNode {
	n := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	n.Options = options
	n.Set.Features[0].Version = label
	return n
}

// Two references to one Feature that differ only in their options install in
// the order TS optionsCompareTo (containerFeaturesOrder.ts) gives them, and
// options it finds equal keep their input order (TS sorts each round with the
// stable Array.prototype.sort).
func TestComputeInstallationOrderSortsByOptions(t *testing.T) {
	for _, tt := range []struct {
		name          string
		first, second any
		want          []string
	}{
		// Fewer keys sort first: aKeys.length - bKeys.length.
		{"fewer keys first", map[string]any{"v": "x"}, map[string]any{}, []string{"second", "first"}},
		// A string value compares with localeCompare.
		{"string values", map[string]any{"v": "y"}, map[string]any{"v": "x"}, []string{"second", "first"}},
		// The first differing key decides, even when a later key differs the
		// other way.
		{"first string key decides", map[string]any{"a": "x", "b": "y"}, map[string]any{"a": "y", "b": "x"}, []string{"first", "second"}},
		// A boolean value: false < true.
		{"boolean values", map[string]any{"v": true}, map[string]any{"v": false}, []string{"second", "first"}},
		{"first boolean key decides", map[string]any{"a": false, "b": true}, map[string]any{"a": true, "b": false}, []string{"first", "second"}},
		// A boolean and a string value are not compared: the objects are equal.
		{"boolean against string value", map[string]any{"v": true}, map[string]any{"v": "x"}, []string{"first", "second"}},
		// Values of different types compare their typeof: "boolean" < "string".
		{"boolean before string", "x", true, []string{"second", "first"}},
		// An empty string is still a string: "boolean" < "string".
		{"boolean before empty string", "", true, []string{"second", "first"}},
		// "object" < "string".
		{"object before string", "x", map[string]any{}, []string{"second", "first"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			first, second := optionsNode("first", tt.first), optionsNode("second", tt.second)
			sets, err := ComputeInstallationOrder(log.Null, []*FNode{first, second})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range sets {
				got = append(got, s.Features[0].Version)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

// A JSON null option value is an object to TS optionsCompareTo, not undefined,
// so it equals a string value and the dependency on the Feature with it is the
// user's Feature: one node, as TS _buildDependencyGraph dedups it.
func TestBuildDependencyGraphNullOptionEqualsAnyValue(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", dependsOn: map[string]any{"ghcr.io/ns/b:1": map[string]any{"v": nil}}},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b"},
	}
	uf := []DevContainerFeature{
		{UserFeatureID: "ghcr.io/ns/a:1", Options: map[string]any{}},
		{UserFeatureID: "ghcr.io/ns/b:1", Options: map[string]any{"v": "x"}},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), uf, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph) != 2 {
		t.Fatalf("worklist size = %d, want 2 (b with null option is b)", len(graph))
	}
}
