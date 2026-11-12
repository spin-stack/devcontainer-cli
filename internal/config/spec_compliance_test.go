package config

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSpecCompliance(t *testing.T) {
	nonModeled := map[string]string{
		"$schema":              "JSON-schema meta key, not a devcontainer.json property",
		"additionalProperties": "JSON-schema keyword surfaced by the walk, not a property",
		"fish":                 "legacy feature key under `features`",
		"maven":                "legacy feature key under `features`",
		"gradle":               "legacy feature key under `features`",
		"jupyterlab":           "legacy feature key under `features`",
		"homebrew":             "legacy feature key under `features`",
		"description":          "sub-property of a `secrets` value",
		"documentationUrl":     "sub-property of a `secrets` value",
		"cores":                "sub-property of hostRequirements.gpu (kept as raw JSON)",
	}

	schemaProps := schemaPropertyNames(t)
	structTags := structJSONTags(reflect.TypeFor[DevContainer]())
	for name := range structJSONTags(reflect.TypeFor[Mount]()) {
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

func schemaPropertyNames(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("testdata/devContainer.base.schema.json")
	if err != nil {
		t.Fatalf("read vendored schema: %v (run `task spec:schema-update`)", err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	out := map[string]bool{}
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if props, ok := n["properties"].(map[string]any); ok {
				for k := range props {
					out[k] = true
				}
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(root)
	return out
}

func structJSONTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	seen := map[reflect.Type]bool{}
	var visit func(t reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
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
		for f := range t.Fields() {
			if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
				out[name] = true
			}
			visit(f.Type)
		}
	}
	visit(t)
	return out
}
