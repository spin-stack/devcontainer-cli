package docker

import (
	"testing"
)

func TestEnsureFinalStageNameReadsAContinuedFrom(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"FROM \\\n    alpine:3.20 AS base\n", "base"},
		{"FROM alpine AS build\nRUN true\nFROM \\\n    build AS final\n", "final"},
	} {
		name, modified, err := EnsureFinalStageName(tc.content, "dev")
		if err != nil || name != tc.want || modified != "" {
			t.Errorf("EnsureFinalStageName(%q) = (%q, %q, %v), want (%q, \"\", nil)", tc.content, name, modified, err, tc.want)
		}
	}
}

func TestEnsureFinalStageNameInsertsTheLabelAfterTheImage(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"FROM alpine:3 # pinned\n", "FROM alpine:3 AS dev # pinned\n"},
		{"# syntax\nFROM alpine # x\nRUN true\n", "# syntax\nFROM alpine AS dev # x\nRUN true\n"},
		{"FROM a AS b\nRUN x\n  FROM --platform=linux/amd64 b # c\n", "FROM a AS b\nRUN x\n  FROM --platform=linux/amd64 b AS dev # c\n"},
	} {
		name, modified, err := EnsureFinalStageName(tc.in, "dev")
		if err != nil || name != "dev" || modified != tc.want {
			t.Errorf("EnsureFinalStageName(%q) = (%q, %q), want (\"dev\", %q)", tc.in, name, modified, tc.want)
		}
	}
}

func TestStageLabelsReadsTheNamedStage(t *testing.T) {
	df := ExtractDockerfile("FROM alpine AS base\nLABEL k=base\nFROM base\nLABEL k=final\n")
	if got := df.StageLabels("base")["k"]; got != "base" {
		t.Errorf("StageLabels(base)[k] = %q, want base", got)
	}
	if got := df.StageLabels("")["k"]; got != "final" {
		t.Errorf("StageLabels(\"\")[k] = %q, want final", got)
	}
}

func TestExtractDockerfileLabelsAContinuedFrom(t *testing.T) {
	df := ExtractDockerfile("FROM alpine \\\n    AS base\nRUN true\n")
	if len(df.Stages) != 1 || df.Stages[0].From.Label != "base" || df.StagesByLabel["base"] == nil {
		t.Errorf("stages = %+v, want one labelled base", df.Stages)
	}
}
