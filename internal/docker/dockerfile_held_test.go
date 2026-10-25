package docker

import (
	"testing"
)

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
