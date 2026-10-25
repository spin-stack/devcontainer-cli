package docker

import (
	"testing"
)

func TestExtractDockerfile(t *testing.T) {
	tests := []struct {
		name               string
		dockerfile         string
		wantStages         int
		wantImages         []string // nil = skip per-stage image check
		wantPreambleVer    string   // "" = skip
		wantPreambleInstrs int      // -1 = skip
		wantFirstInstrName string   // "" = skip
	}{
		{
			name:               "single_stage",
			dockerfile:         "FROM ubuntu:latest\nRUN apt-get update\n",
			wantStages:         1,
			wantImages:         []string{"ubuntu:latest"},
			wantPreambleInstrs: -1,
		},
		{
			name:               "empty_string",
			dockerfile:         "",
			wantStages:         0,
			wantPreambleInstrs: -1,
		},
		{
			name:               "only_preamble",
			dockerfile:         "# Just a comment\nARG VERSION=1.0\n",
			wantStages:         0,
			wantPreambleInstrs: 1,
		},
		{
			name: "multistage_count",
			dockerfile: `FROM debian:latest as base
FROM ubuntu:latest as dev
`,
			wantStages:         2,
			wantImages:         []string{"debian:latest", "ubuntu:latest"},
			wantPreambleInstrs: -1,
		},
		{
			name: "with_preamble",
			dockerfile: `# syntax=docker/dockerfile:1.4
ARG BASE=ubuntu
FROM ${BASE}
`,
			wantStages:         1,
			wantPreambleVer:    "1.4",
			wantPreambleInstrs: 1,
			wantFirstInstrName: "BASE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			df := ExtractDockerfile(tt.dockerfile)
			if len(df.Stages) != tt.wantStages {
				t.Fatalf("stages = %d, want %d", len(df.Stages), tt.wantStages)
			}
			for i, img := range tt.wantImages {
				if df.Stages[i].From.Image != img {
					t.Errorf("stage%d image = %q, want %q", i, df.Stages[i].From.Image, img)
				}
			}
			if tt.wantPreambleVer != "" && df.Preamble.Version != tt.wantPreambleVer {
				t.Errorf("version = %q, want %q", df.Preamble.Version, tt.wantPreambleVer)
			}
			if tt.wantPreambleInstrs != -1 && len(df.Preamble.Instructions) != tt.wantPreambleInstrs {
				t.Errorf("preamble instructions = %d, want %d", len(df.Preamble.Instructions), tt.wantPreambleInstrs)
			}
			if tt.wantFirstInstrName != "" && df.Preamble.Instructions[0].Name != tt.wantFirstInstrName {
				t.Errorf("preamble arg name = %q, want %q", df.Preamble.Instructions[0].Name, tt.wantFirstInstrName)
			}
		})
	}
}

// TestStageLabelsDevcontainerMetadata guards #1225: the parser must expose a
// `LABEL devcontainer.metadata` from the (final) build stage so the CLI can
// preserve it instead of clobbering it with its own computed label.
func TestStageLabelsDevcontainerMetadata(t *testing.T) {
	df := ExtractDockerfile("FROM ubuntu:22.04\n" +
		"LABEL maintainer=me\n" +
		"LABEL devcontainer.metadata='[{\"remoteUser\":\"foo\"}]'\n" +
		"RUN echo hi\n")
	labels := df.StageLabels("")
	if got := labels["devcontainer.metadata"]; got != `[{"remoteUser":"foo"}]` {
		t.Errorf("devcontainer.metadata label = %q", got)
	}
	if labels["maintainer"] != "me" {
		t.Errorf("maintainer label = %q", labels["maintainer"])
	}

	// A Dockerfile without the label yields no metadata entry.
	df2 := ExtractDockerfile("FROM ubuntu:22.04\nRUN echo hi\n")
	if v, ok := df2.StageLabels("")["devcontainer.metadata"]; ok {
		t.Errorf("unexpected devcontainer.metadata = %q", v)
	}
}
