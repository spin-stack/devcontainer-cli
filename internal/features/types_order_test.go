package features

import (
	"encoding/json"
	"reflect"
	"testing"
)

// DependsOnKeys is TS Object.keys(dependsOn): the metadata's order, a
// repeated key where it first appears, and keys set in code (not in the
// metadata) after them, sorted.
func TestDependsOnKeysKeepsTheMetadataOrder(t *testing.T) {
	var f Feature
	if err := json.Unmarshal([]byte(`{"id":"x","dependsOn":{"b":{},"a":{},"b":{"v":1}}}`), &f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.DependsOnKeys(), []string{"b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys = %v, want %v", got, want)
	}

	f = Feature{DependsOn: map[string]any{"c": nil, "z": nil, "a": nil}, DependsOnOrder: []string{"gone", "c"}}
	if got, want := f.DependsOnKeys(), []string{"c", "a", "z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys = %v, want %v", got, want)
	}
}

// A Feature with SourceKeys is written as TS builds it: the metadata spread
// over the source's properties, cachePath and consecutiveId added, that
// spread over the devcontainer-feature.json, deprecated extensions and
// settings moved. The metadata's properties are its own, those Go has no
// field for and an empty options included; without SourceKeys the Feature
// keeps the struct's order.
func TestFeatureMarshalJSONBuildsTheFeatureAsTS(t *testing.T) {
	var f Feature
	meta := `{"version":"1.0.0","containerEnv":{"K":"v"},"id":"x","options":{},"keywords":["k"],"version":"1.0.1","10":1,"2":2}`
	if err := json.Unmarshal([]byte(meta), &f); err != nil {
		t.Fatal(err)
	}
	f.Name, f.Value, f.Included, f.CachePath, f.ConsecutiveId = "./x", map[string]any{"o": "v"}, true, "/c", "x_0"
	f.CapAdd = []string{"SET_IN_CODE"} // not the metadata's: TS has no such property
	for _, tc := range []struct {
		name   string
		source []string
		file   string
		want   string
	}{
		{"local", LocalFeatureKeys, "", `{"2":2,"10":1,"version":"1.0.1","containerEnv":{"K":"v"},"id":"x","options":{},"keywords":["k"],"name":"./x","value":{"o":"v"},"included":true,"cachePath":"/c","consecutiveId":"x_0"}`},
		{"oci", OCIFeatureKeys, "", `{"2":2,"10":1,"version":"1.0.1","containerEnv":{"K":"v"},"id":"x","options":{},"keywords":["k"],"included":true,"value":{"o":"v"},"cachePath":"/c","consecutiveId":"x_0"}`},
		// The file's properties come first, then the Feature's in its order; the
		// metadata's values win.
		{"oci with a layer file", OCIFeatureKeys, `{"id":"x","onlyInFile":true,"version":"0.9.0"}`, `{"2":2,"10":1,"id":"x","onlyInFile":true,"version":"1.0.1","included":true,"value":{"o":"v"},"containerEnv":{"K":"v"},"options":{},"keywords":["k"],"cachePath":"/c","consecutiveId":"x_0"}`},
		{"struct", nil, "", `{"id":"x","version":"1.0.1","name":"./x","included":true,"cachePath":"/c","consecutiveId":"x_0","value":{"o":"v"},"containerEnv":{"K":"v"},"capAdd":["SET_IN_CODE"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := f
			g.SourceKeys = tc.source
			if tc.file != "" {
				g.File = json.RawMessage(tc.file)
			}
			got, err := json.Marshal(g)
			if err != nil || string(got) != tc.want {
				t.Errorf("got  %s, %v\nwant %s", got, err, tc.want)
			}
		})
	}
}

// TS's updateFromOldProperties for Features moves a truthy extensions and
// settings into customizations.vscode: extensions concatenated after vscode's
// (a string base concatenates as a string), settings spread under vscode's;
// devPort is left alone.
func TestFeatureMarshalJSONMovesDeprecatedProperties(t *testing.T) {
	for _, tc := range []struct{ meta, want string }{
		{`{"id":"a","extensions":["x"],"settings":{"s":1,"t":1},"devPort":1,"customizations":{"vscode":{"extensions":["e"],"settings":{"t":2}}}}`,
			`{"id":"a","devPort":1,"customizations":{"vscode":{"extensions":["e","x"],"settings":{"s":1,"t":2}}},"included":true,"value":null}`},
		{`{"id":"b","extensions":"solo","settings":[7]}`,
			`{"id":"b","included":true,"value":null,"customizations":{"vscode":{"extensions":["solo"],"settings":{"0":7}}}}`},
		{`{"id":"c","extensions":["x",1],"customizations":{"vscode":{"extensions":"base"}}}`,
			`{"id":"c","customizations":{"vscode":{"extensions":"basex,1"}},"included":true,"value":null}`},
		{`{"id":"d","extensions":[],"settings":"ab"}`,
			`{"id":"d","included":true,"value":null,"customizations":{"vscode":{"extensions":[],"settings":{"0":"a","1":"b"}}}}`},
		{`{"id":"e","extensions":"","settings":null,"devPort":1}`,
			`{"id":"e","extensions":"","settings":null,"devPort":1,"included":true,"value":null}`},
	} {
		var f Feature
		if err := json.Unmarshal([]byte(tc.meta), &f); err != nil {
			t.Fatal(err)
		}
		f.Included, f.SourceKeys = true, OCIFeatureKeys
		if got, err := json.Marshal(f); err != nil || string(got) != tc.want {
			t.Errorf("%s:\ngot  %s, %v\nwant %s", tc.meta, got, err, tc.want)
		}
	}
	var f Feature
	if err := json.Unmarshal([]byte(`{"id":"f","extensions":["x"],"customizations":{"vscode":{"extensions":3}}}`), &f); err != nil {
		t.Fatal(err)
	}
	f.SourceKeys = OCIFeatureKeys
	if _, err := json.Marshal(f); err == nil {
		t.Error("extensions concatenated onto a number, which has no concat: want an error")
	}
}
