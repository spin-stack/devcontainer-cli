package features

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestJSObjectRoundTripsAsJavaScript(t *testing.T) {
	src := `{"b":1,"4294967295":2,"10":3,"01":4,"2":5,"b":6,"-1":7,"4294967296":8,"9":9,"4294967294":10,"a":[{"z":1,"y":[]}],"n":1.50,"e":1e21}`
	v, err := parseJS([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(v)
	want := `{"2":5,"9":9,"10":3,"4294967294":10,"b":6,"4294967295":2,"01":4,"-1":7,"4294967296":8,"a":[{"z":1,"y":[]}],"n":1.5,"e":1e+21}`
	if err != nil || string(got) != want {
		t.Errorf("got  %s, %v\nwant %s", got, err, want)
	}
	for _, bad := range []string{`{"a":}`, `{"a":1} 2`, ``, `[1,`} {
		if _, err := parseJS([]byte(bad)); err == nil {
			t.Errorf("parseJS(%q): no error", bad)
		}
	}
}

func TestManifestJSON(t *testing.T) {
	got, _ := json.Marshal(ManifestJSON([]byte(`{"schemaVersion":2,"config":{"mediaType":"m","digest":"d"}}`), nil))
	if string(got) != `{"schemaVersion":2,"config":{"mediaType":"m","digest":"d"}}` {
		t.Errorf("manifest = %s", got)
	}
	if got := ManifestJSON(nil, "fallback"); got != "fallback" {
		t.Errorf("unparsable body: %v, want the fallback", got)
	}
}

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

func TestPropertyOrderListsKeysAsObjectKeys(t *testing.T) {
	m := map[string]int{"PATH": 1, "SDKMAN_DIR": 2, "10": 3, "2": 4, "b": 5, "a": 6}
	got := PropertyOrder(m, []string{"SDKMAN_DIR", "gone", "10", "PATH", "SDKMAN_DIR", "2"})
	if want := []string{"2", "10", "SDKMAN_DIR", "PATH", "a", "b"}; !slices.Equal(got, want) {
		t.Errorf("PropertyOrder = %v, want %v", got, want)
	}
	if got := PropertyOrder(map[string]int{}, []string{"x"}); len(got) != 0 {
		t.Errorf("PropertyOrder of an empty map = %v", got)
	}
}
