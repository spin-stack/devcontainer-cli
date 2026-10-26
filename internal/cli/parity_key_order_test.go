package cli

import (
	"slices"
	"testing"
)

// A key_order path reaches into arrays by index, so a case can pin the key
// order of an element (featuresConfiguration.featureSets.0.features.0).
func TestJSONKeyOrderIndexesArrays(t *testing.T) {
	doc := `{"a":[{"x":1},{"z":1,"y":2}]}`
	if keys, err := jsonKeyOrder(doc, "a.1"); err != nil || !slices.Equal(keys, []string{"z", "y"}) {
		t.Errorf("a.1: keys = %v, %v; want [z y]", keys, err)
	}
	for _, path := range []string{"a.2", "a.-1", "a.0.x.0"} {
		if _, err := jsonKeyOrder(doc, path); err == nil {
			t.Errorf("%s: no error, want one", path)
		}
	}
}
