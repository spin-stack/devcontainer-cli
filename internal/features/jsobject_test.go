package features

import (
	"encoding/json"
	"testing"
)

// JSON.parse then JSON.stringify: a repeated property keeps its first
// position and its last value, array-index properties come first, ascending
// (4294967295, "01" and "-1" are not indexes), the rest in insertion order.
func TestJSObjectRoundTripsAsJavaScript(t *testing.T) {
	src := `{"b":1,"4294967295":2,"10":3,"01":4,"2":5,"b":6,"-1":7,"a":[{"z":1,"y":[]}],"n":1.50,"e":1e21}`
	v, err := parseJS([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(v)
	want := `{"2":5,"10":3,"b":6,"4294967295":2,"01":4,"-1":7,"a":[{"z":1,"y":[]}],"n":1.5,"e":1e+21}`
	if err != nil || string(got) != want {
		t.Errorf("got  %s, %v\nwant %s", got, err, want)
	}
	for _, bad := range []string{`{"a":}`, `{"a":1} 2`, ``, `[1,`} {
		if _, err := parseJS([]byte(bad)); err == nil {
			t.Errorf("parseJS(%q): no error", bad)
		}
	}
}

// A manifest is written as the registry served it, or as the fallback when
// its body does not parse.
func TestManifestJSON(t *testing.T) {
	got, _ := json.Marshal(ManifestJSON([]byte(`{"schemaVersion":2,"config":{"mediaType":"m","digest":"d"}}`), nil))
	if string(got) != `{"schemaVersion":2,"config":{"mediaType":"m","digest":"d"}}` {
		t.Errorf("manifest = %s", got)
	}
	if got := ManifestJSON(nil, "fallback"); got != "fallback" {
		t.Errorf("unparsable body: %v, want the fallback", got)
	}
}

// Metadata that is not an object spreads as TS spreads it: an array by index,
// a number not at all; none at all is an empty object.
func TestMetadataObject(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{``, `{}`},
		{`[5,6]`, `{"0":5,"1":6}`},
		{`3`, `{}`},
		{`{"a":1}`, `{"a":1}`},
	} {
		obj, err := metadataObject(json.RawMessage(tc.data))
		if err != nil {
			t.Fatalf("%q: %v", tc.data, err)
		}
		if got, _ := json.Marshal(obj); string(got) != tc.want {
			t.Errorf("%q: %s, want %s", tc.data, got, tc.want)
		}
	}
	if _, err := metadataObject(json.RawMessage(`{`)); err == nil {
		t.Error("broken metadata: no error")
	}
}
