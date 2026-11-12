package config

import (
	"encoding/json"
	"testing"
)

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
