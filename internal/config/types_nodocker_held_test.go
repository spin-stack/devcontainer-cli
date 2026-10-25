package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TS updateFromOldProperties moves a property by JavaScript truthiness
// (`if (copy.extensions)`), and devPort whenever it is not undefined, so a
// null devPort moves too; an empty string or 0 stays where it is.
func TestUpdateFromOldPropertiesFollowsJavaScriptTruthiness(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`{"devPort":null}`, `{"customizations":{"vscode":{"devPort":null}}}`},
		{`{"extensions":""}`, `{"extensions":""}`},
		{`{"extensions":"a"}`, `{"customizations":{"vscode":{"extensions":["a"]}}}`},
		{`{"extensions":0}`, `{"extensions":0}`},
		{`{"extensions":1}`, `{"customizations":{"vscode":{"extensions":[1]}}}`},
		{`{"extensions":false,"settings":0}`, `{"extensions":false,"settings":0}`},
	} {
		var in, want map[string]any
		if err := json.Unmarshal([]byte(tt.in), &in); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
			t.Fatal(err)
		}
		if got := updateFromOldProperties(in); !reflect.DeepEqual(got, want) {
			g, _ := json.Marshal(got)
			t.Errorf("updateFromOldProperties(%s) = %s, want %s", tt.in, g, tt.want)
		}
	}
}
