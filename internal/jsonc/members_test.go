package jsonc

import (
	"slices"
	"testing"
)

// MemberNames keeps the document's order, which the disallowed-Feature check
// needs to report the first feature as TS Object.keys does; a field that is not
// an object, a missing field, or a document that is not an object gives nil.
func TestMemberNamesKeepsDocumentOrder(t *testing.T) {
	doc := []byte("\ufeff{\n // comment\n \"image\": \"x\",\n \"features\": {\"z\": {}, \"a\": true, \"m\": {},},\n}")
	if got := MemberNames(doc, "features"); !slices.Equal(got, []string{"z", "a", "m"}) {
		t.Errorf("MemberNames = %v, want [z a m]", got)
	}
	for name, d := range map[string]string{
		"field not an object": `{"features": []}`,
		"no field":            `{"image": "x"}`,
		"not an object":       `[]`,
		"not JSON":            `{`,
	} {
		if got := MemberNames([]byte(d), "features"); got != nil {
			t.Errorf("%s: MemberNames = %v, want nil", name, got)
		}
	}
}

// Names keeps the top-level object's document order; a document that is not
// an object, or not JSON, gives nil.
func TestNamesKeepsDocumentOrder(t *testing.T) {
	doc := []byte("\ufeff{\n // comment\n \"name\": \"x\",\n \"image\": \"y\",\n \"capAdd\": [],\n}")
	if got := Names(doc); !slices.Equal(got, []string{"name", "image", "capAdd"}) {
		t.Errorf("Names = %v, want [name image capAdd]", got)
	}
	for _, d := range []string{`[]`, `{`} {
		if got := Names([]byte(d)); got != nil {
			t.Errorf("Names(%s) = %v, want nil", d, got)
		}
	}
}
