package docker

import "testing"

func TestDockerfileResolverVariableExpressions(t *testing.T) {
	for _, tc := range []struct {
		name, dockerfile string
		want             string
	}{
		{"default word, variable set", "ARG V=set\nFROM img:${V:-fallback}\n", "img:set"},
		{"default word, variable unset", "ARG V\nFROM img:${V:-fallback}\n", "img:fallback"},
		{"alternate word, variable set", "ARG V=set\nFROM img:${V:+alt}\n", "img:alt"},
		{"alternate word, variable unset", "ARG V\nFROM img:x${V:+alt}\n", "img:x"},
		{"quoted word", "ARG V\nFROM img:${V:-\"quoted\"}\n", "img:quoted"},
		{"plain variable", "ARG V=1\nFROM img:$V-${V}\n", "img:1-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewResolver(ExtractDockerfile(tc.dockerfile), nil, nil)
			if got := r.FindBaseImage(""); got != tc.want {
				t.Errorf("FindBaseImage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDockerfileResolverParentStageArgIsNotInherited(t *testing.T) {
	// An ARG of the stage a stage is built FROM is not in scope, its ENV is, and
	// so is a global ARG (TS findValue considers ARGs only in the stage itself
	// and in the global scope).
	r := NewResolver(ExtractDockerfile("ARG G=global\nFROM img AS base\nARG U=parent\nENV E=env\nFROM base\nUSER ${U:-none}-${E}-${G}\n"), nil, nil)
	if got := r.FindUserStatement(nil, ""); got != "none-env-global" {
		t.Fatalf("FindUserStatement = %q, want %q", got, "none-env-global")
	}
}

func TestTrimOneQuote(t *testing.T) {
	for in, want := range map[string]string{
		``:       ``,
		`"`:      ``,
		`'`:      ``,
		`a`:      `a`,
		`"ab"`:   `ab`,
		`'ab'`:   `ab`,
		`"ab`:    `ab`,
		`ab"`:    `ab`,
		`'ab`:    `ab`,
		`ab'`:    `ab`,
		`""ab""`: `"ab"`,
	} {
		if got := trimOneQuote(in); got != want {
			t.Errorf("trimOneQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
