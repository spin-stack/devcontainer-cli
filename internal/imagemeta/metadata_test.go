package imagemeta

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The label is JSON.stringify of the entries: &, < and > are written as they
// are, and each entry's properties in TS's pick order.
func TestGenerateMetadataLabelWritesWhatJSONStringifyWrites(t *testing.T) {
	init := true
	got := GenerateMetadataLabel([]Entry{
		{ID: "f", Entrypoint: "/e.sh", Init: &init, OnCreateCommand: "a && b"},
		{RemoteUser: "u", PostCreateCommand: "echo <x> > /y", ContainerEnv: map[string]string{"A": "1"}},
	})
	want := `[{"id":"f","onCreateCommand":"a && b","init":true,"entrypoint":"/e.sh"},{"postCreateCommand":"echo <x> > /y","containerEnv":{"A":"1"},"remoteUser":"u"}]`
	if got != want {
		t.Errorf("label = %s\nwant    %s", got, want)
	}
	if got := GenerateMetadataLabel([]Entry{{}}); got != "" {
		t.Errorf("label without entries = %q, want none", got)
	}
}

// configEntryKeys orders every property of an Entry but id, so MarshalJSON
// writes all of them.
func TestEntryKeyOrdersNameEveryProperty(t *testing.T) {
	typ := reflect.TypeFor[Entry]()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "id" && !slices.Contains(configEntryKeys, name) {
			t.Errorf("property %q is in no key order", name)
		}
	}
	if !slices.Contains(featureEntryKeys, "id") {
		t.Error("featureEntryKeys has no id")
	}
}
