package config

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSpecCompliance gates devcontainer.json schema compliance: every property
// defined in the official devContainer.base.schema.json MUST be modeled by a
// json tag somewhere in the config struct tree, so nothing the spec defines is
// silently dropped on parse / read-configuration / metadata round-trip.
//
// This is the config analog of TestFlagInventoryParity. It excludes our Go-only
// additions by construction: it only fails when a SPEC property is missing from
// our structs, never when we carry extra fields the spec doesn't define.
//
// The schema is vendored (pinned) at testdata/devContainer.base.schema.json from
// github.com/devcontainers/spec (schemas/devContainer.base.schema.json). Refresh
// it with `task spec:schema-update`; a new spec property then fails this test
// until it is added to the struct — that is the "always following the spec" gate.
func TestSpecCompliance(t *testing.T) {
	// nonModeled lists schema property names we deliberately do not model as
	// dedicated struct fields, each with the reason. Everything else MUST map to
	// a json tag. Keep this list tiny and justified — a genuinely new config
	// property must NOT be added here to silence the test; add the struct field.
	nonModeled := map[string]string{
		"$schema":              "JSON-schema meta key, not a devcontainer.json property",
		"additionalProperties": "JSON-schema keyword surfaced by the walk, not a property",
		// Deprecated legacy feature keys nested under `features` (an open map we
		// preserve wholesale via Features map[string]interface{}).
		"fish":       "legacy feature key under `features`",
		"maven":      "legacy feature key under `features`",
		"gradle":     "legacy feature key under `features`",
		"jupyterlab": "legacy feature key under `features`",
		"homebrew":   "legacy feature key under `features`",
		// Sub-properties of a `secrets` value (open map, preserved wholesale).
		"description":      "sub-property of a `secrets` value",
		"documentationUrl": "sub-property of a `secrets` value",
		// Sub-property of hostRequirements.gpu, which we preserve as raw JSON.
		"cores": "sub-property of hostRequirements.gpu (kept as raw JSON)",
	}

	schemaProps := schemaPropertyNames(t)
	structTags := structJSONTags(reflect.TypeOf(DevContainer{}))
	// MountOrString wraps its value in an interface, so reflection can't reach
	// the Mount struct through it — seed it explicitly.
	for name := range structJSONTags(reflect.TypeOf(Mount{})) {
		structTags[name] = true
	}

	var missing []string
	for name := range schemaProps {
		if _, skip := nonModeled[name]; skip {
			continue
		}
		if !structTags[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("devcontainer.json schema properties not modeled by config structs (add the field, or justify in nonModeled): %v", missing)
	}
}

// schemaPropertyNames returns every property name defined anywhere under a
// "properties" object in the vendored schema.
func schemaPropertyNames(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("testdata/devContainer.base.schema.json")
	if err != nil {
		t.Fatalf("read vendored schema: %v (run `task spec:schema-update`)", err)
	}
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	out := map[string]bool{}
	var walk func(node interface{})
	walk = func(node interface{}) {
		switch n := node.(type) {
		case map[string]interface{}:
			if props, ok := n["properties"].(map[string]interface{}); ok {
				for k := range props {
					out[k] = true
				}
			}
			for _, v := range n {
				walk(v)
			}
		case []interface{}:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(root)
	return out
}

// structJSONTags collects every json tag name reachable from t (recursing into
// struct fields, pointers, slices/arrays and map values).
func structJSONTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	seen := map[reflect.Type]bool{}
	var visit func(t reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			t = t.Elem()
		}
		if t.Kind() == reflect.Map {
			visit(t.Elem())
			return
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if name := strings.Split(f.Tag.Get("json"), ",")[0]; name != "" && name != "-" {
				out[name] = true
			}
			visit(f.Type)
		}
	}
	visit(t)
	return out
}
