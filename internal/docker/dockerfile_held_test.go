package docker

import (
	"slices"
	"testing"
)

// A FROM continued onto the next line is one instruction: BuildKit's parser
// joins it, so the stage name on the continuation line is found and the
// Dockerfile is left alone. The TS regex (dockerfileUtils.ts
// ensureDockerfileHasFinalStageName) reads only the physical "FROM \" line;
// EnsureFinalStageName consults BuildKit first for exactly this case.
func TestEnsureFinalStageNameReadsAContinuedFrom(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"FROM \\\n    alpine:3.20 AS base\n", "base"},
		{"FROM alpine AS build\nRUN true\nFROM \\\n    build AS final\n", "final"},
	} {
		name, modified := EnsureFinalStageName(tc.content, "dev")
		if name != tc.want || modified != "" {
			t.Errorf("EnsureFinalStageName(%q) = (%q, %q), want (%q, \"\")", tc.content, name, modified, tc.want)
		}
	}
}

// When BuildKit rejects the file (a trailing comment after the image), the
// label goes right after the image of the last FROM, at the same byte offset
// TS computes from the match index (dockerfileUtils.ts: `dockerfile.slice(0,
// lastLineStartIndex + matchedFromText.length) + " AS " + name`).
func TestEnsureFinalStageNameInsertsTheLabelAfterTheImage(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"FROM alpine:3 # pinned\n", "FROM alpine:3 AS dev # pinned\n"},
		{"# syntax\nFROM alpine # x\nRUN true\n", "# syntax\nFROM alpine AS dev # x\nRUN true\n"},
		{"FROM a AS b\nRUN x\n  FROM --platform=linux/amd64 b # c\n", "FROM a AS b\nRUN x\n  FROM --platform=linux/amd64 b AS dev # c\n"},
	} {
		name, modified := EnsureFinalStageName(tc.in, "dev")
		if name != "dev" || modified != tc.want {
			t.Errorf("EnsureFinalStageName(%q) = (%q, %q), want (\"dev\", %q)", tc.in, name, modified, tc.want)
		}
	}
}

// isFromLine decides which line EnsureFinalStageName's fallback treats as a
// FROM: one whose first token, case-insensitively, is FROM followed by a blank,
// a line end or nothing.
func TestIsFromLineWantsFromAsTheWholeFirstToken(t *testing.T) {
	for line, want := range map[string]bool{
		"FROM":          true,
		"FROM\n":        true,
		"FROM\r\n":      true,
		"FROM alpine":   true,
		"FROM\talpine":  true,
		"  \tfrom x\n":  true,
		"FROMAGE x":     false,
		"FROM-x":        false,
		"FRO":           false,
		"RUN FROM x":    false,
		"# FROM alpine": false,
	} {
		if got := isFromLine(line); got != want {
			t.Errorf("isFromLine(%q) = %v, want %v", line, got, want)
		}
	}
}

// StageLabels reads the named stage, not the final one, when a name is given,
// and the final stage when the name is empty.
func TestStageLabelsReadsTheNamedStage(t *testing.T) {
	df := ExtractDockerfile("FROM alpine AS base\nLABEL k=base\nFROM base\nLABEL k=final\n")
	if got := df.StageLabels("base")["k"]; got != "base" {
		t.Errorf("StageLabels(base)[k] = %q, want base", got)
	}
	if got := df.StageLabels("")["k"]; got != "final" {
		t.Errorf("StageLabels(\"\")[k] = %q, want final", got)
	}
}

// mapEnv is the EnvGetter FindBaseImage hands BuildKit's lexer; its keys are
// exactly the map's.
func TestMapEnvKeysAreTheMapKeys(t *testing.T) {
	got := mapEnv{"A": "1", "B": ""}.Keys()
	slices.Sort(got)
	if want := []string{"A", "B"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %q, want %q", got, want)
	}
}

// A `# escape=` directive changes how the FROM line is lexed: with a backtick
// escape, "\" is literal and "`$" is a literal dollar, as in Docker.
func TestFindBaseImageHonoursTheEscapeDirective(t *testing.T) {
	df := ExtractDockerfile("# escape=`\nFROM reg`$x/img\n")
	if got := FindBaseImage(df, nil, ""); got != "reg$x/img" {
		t.Errorf("FindBaseImage = %q, want reg$x/img", got)
	}
}
