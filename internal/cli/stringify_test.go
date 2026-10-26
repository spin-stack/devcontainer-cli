package cli

import (
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

// JSON.stringify writes &, <, > and U+2028/U+2029 as they are, and every
// other escape as encoding/json does.
func TestStringifyJSONWritesWhatJSONStringifyWrites(t *testing.T) {
	cases := []struct {
		name   string
		v      any
		indent string
		want   string
	}{
		{"html characters", "a && b <x>", "", "\"a && b <x>\""},
		{"line and paragraph separators", "\u2028\u2029", "", "\"\u2028\u2029\""},
		{"an escaped backslash before u0026", `\u0026`, "", `"\\u0026"`},
		{"an escaped quote at the end", `"`, "", `"\""`},
		{"control characters", "\b\f\n\r\t\x01", "", `"\b\f\n\r\t\u0001"`},
		{"a nested marshaler", orderedObject{map[string]any{"<k>": "&"}, nil}, "", `{"<k>":"&"}`},
		{"indented", map[string]any{"k": "<v>"}, "  ", "{\n  \"k\": \"<v>\"\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stringifyJSON(tc.v, tc.indent)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("stringifyJSON = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStringifyJSONReportsAnUnencodableValue(t *testing.T) {
	if _, err := stringifyJSON(make(chan int), ""); err == nil {
		t.Error("stringifyJSON of a channel succeeded")
	}
	if _, err := stringifyJSON(make(chan int), "  "); err == nil {
		t.Error("indented stringifyJSON of a channel succeeded")
	}
}

// A command's error result is written as JSON.stringify writes it.
func TestWriteErrorJSONDoesNotEscapeHTML(t *testing.T) {
	var out captureOutput
	_ = writeErrorJSON(&out, coreerrors.ErrorOutput{Outcome: "error", Message: "a && <b>", Description: "a && <b>"})
	want := `{"outcome":"error","message":"a && <b>","description":"a && <b>"}` + "\n"
	if got := out.out.String(); got != want {
		t.Errorf("writeErrorJSON wrote %q, want %q", got, want)
	}
}
