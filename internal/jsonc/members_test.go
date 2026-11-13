package jsonc

import (
	"slices"
	"testing"
)

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

func TestMemberNamesFollowsAPath(t *testing.T) {
	doc := []byte(`{"features": {"./f": {"zz": 1, /* c */ "aa": 2}, "./g": true}}`)
	if got := MemberNames(doc, "features", "./f"); !slices.Equal(got, []string{"zz", "aa"}) {
		t.Errorf("MemberNames(features, ./f) = %v, want [zz aa]", got)
	}
	for _, path := range [][]string{{"features", "./g"}, {"features", "./h"}, {"features", "./f", "zz"}} {
		if got := MemberNames(doc, path...); got != nil {
			t.Errorf("MemberNames(%v) = %v, want nil", path, got)
		}
	}
}

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
