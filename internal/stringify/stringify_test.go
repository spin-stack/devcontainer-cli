package stringify

import "testing"

// rawMarshaler writes its JSON itself, as the CLI's ordered result objects do.
type rawMarshaler string

func (r rawMarshaler) MarshalJSON() ([]byte, error) { return []byte(r), nil }

// JSON.stringify writes &, <, > and U+2028/U+2029 as they are, and every
// other escape as encoding/json does.
func TestJSONWritesWhatJSONStringifyWrites(t *testing.T) {
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
		{"a nested marshaler", map[string]any{"k": rawMarshaler(`{"<k>":"&"}`)}, "", `{"k":{"<k>":"&"}}`},
		{"indented", map[string]any{"k": "<v>"}, "  ", "{\n  \"k\": \"<v>\"\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JSON(tc.v, tc.indent)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("JSON = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestJSONReportsAnUnencodableValue(t *testing.T) {
	if _, err := JSON(make(chan int), ""); err == nil {
		t.Error("JSON of a channel succeeded")
	}
	if _, err := JSON(make(chan int), "  "); err == nil {
		t.Error("indented JSON of a channel succeeded")
	}
}
