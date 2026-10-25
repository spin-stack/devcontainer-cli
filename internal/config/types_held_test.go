package config

import (
	"encoding/json"
	"testing"
)

// The build context is the legacy top-level "context", else build.context, else
// none (TS getDockerContextPath, spec-node/utils.ts: `'dockerFile' in config ?
// config.context : config.build.context`).
func TestBuildContextPrefersTheLegacyProperty(t *testing.T) {
	for in, want := range map[string]string{
		`{"dockerFile":"D","context":"legacy","build":{"context":"build"}}`: "legacy",
		`{"build":{"context":".."}}`:                                        "..",
		`{"build":{"dockerfile":"Dockerfile"}}`:                             "",
		`{}`:                                                                "",
	} {
		var c DevContainer
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatal(err)
		}
		if got := c.BuildContext(); got != want {
			t.Errorf("BuildContext(%s) = %q, want %q", in, got, want)
		}
	}
}

// A lifecycle command marshals back as it was written - the string, array or
// object TS keeps as-is in the metadata label - and an absent one as null.
func TestLifecycleCommandMarshalsAsWritten(t *testing.T) {
	for _, in := range []string{`"echo hi"`, `["echo","hi"]`, `{"a":"echo a"}`, `null`} {
		var c LifecycleCommand
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != in {
			t.Errorf("marshal(%s) = %s, want it unchanged", in, got)
		}
	}
	if got, err := json.Marshal(LifecycleCommand{}); err != nil || string(got) != "null" {
		t.Errorf("marshal of no command = %s, %v; want null", got, err)
	}
}

// A single dockerComposeFile marshals as the string, more than one as the array
// (the `string | string[]` shape the spec gives the property).
func TestStringOrStringsMarshalsOneAsAString(t *testing.T) {
	for in, want := range map[string]string{
		`"a.yml"`:           `"a.yml"`,
		`["a.yml"]`:         `"a.yml"`,
		`["a.yml","b.yml"]`: `["a.yml","b.yml"]`,
	} {
		var s StringOrStrings
		if err := json.Unmarshal([]byte(in), &s); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("marshal(%s) = %s, want %s", in, got, want)
		}
	}
}
