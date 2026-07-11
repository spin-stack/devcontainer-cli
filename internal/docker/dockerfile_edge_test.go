package docker

import (
	"testing"
)

func TestExtractDockerfile_SingleStage(t *testing.T) {
	df := ExtractDockerfile("FROM ubuntu:latest\nRUN apt-get update\n")
	if len(df.Stages) != 1 {
		t.Errorf("stages = %d", len(df.Stages))
	}
	if df.Stages[0].From.Image != "ubuntu:latest" {
		t.Errorf("image = %q", df.Stages[0].From.Image)
	}
}

func TestExtractDockerfile_EmptyString(t *testing.T) {
	df := ExtractDockerfile("")
	if len(df.Stages) != 0 {
		t.Errorf("stages = %d for empty Dockerfile", len(df.Stages))
	}
}

func TestExtractDockerfile_OnlyPreamble(t *testing.T) {
	df := ExtractDockerfile("# Just a comment\nARG VERSION=1.0\n")
	if len(df.Stages) != 0 {
		t.Errorf("stages = %d", len(df.Stages))
	}
	if len(df.Preamble.Instructions) != 1 {
		t.Errorf("preamble instructions = %d", len(df.Preamble.Instructions))
	}
}

func TestFindBaseImage_WithPlatform(t *testing.T) {
	dockerfile := `FROM --platform=linux/amd64 ubuntu:22.04
RUN echo hello
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "ubuntu:22.04" {
		t.Errorf("image = %q", image)
	}
	if df.Stages[0].From.Platform != "linux/amd64" {
		t.Errorf("platform = %q", df.Stages[0].From.Platform)
	}
}

func TestFindBaseImage_EnvVariable(t *testing.T) {
	dockerfile := `FROM debian
ENV BASE=custom-image
FROM $BASE
`
	df := ExtractDockerfile(dockerfile)
	// ENV in a FROM stage doesn't affect FROM resolution of that stage
	// but the second stage has FROM $BASE which resolves from preamble ARGs
	if len(df.Stages) != 2 {
		t.Fatalf("stages = %d", len(df.Stages))
	}
}

func TestFindUserStatement_MultipleUsers(t *testing.T) {
	dockerfile := `FROM debian
USER first
USER second
USER third
`
	df := ExtractDockerfile(dockerfile)
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "")
	if user != "third" {
		t.Errorf("user = %q, want 'third' (last USER wins)", user)
	}
}

func TestSupportsBuildContexts_V2(t *testing.T) {
	df := ExtractDockerfile("# syntax=docker/dockerfile:2\nFROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if !ok || unknown {
		t.Errorf("v2 should support build contexts: ok=%v unknown=%v", ok, unknown)
	}
}

func TestSupportsBuildContexts_Labs(t *testing.T) {
	df := ExtractDockerfile("# syntax=docker/dockerfile:labs\nFROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if !ok || unknown {
		t.Errorf("labs should support: ok=%v unknown=%v", ok, unknown)
	}
}

func TestEnsureFinalStageName_SingleStage(t *testing.T) {
	dockerfile := "FROM ubuntu:latest\nRUN echo hello\n"
	name, modified := EnsureDockerfileHasFinalStageName(dockerfile, "myname")
	if name != "myname" {
		t.Errorf("name = %q", name)
	}
	if modified == "" {
		t.Error("expected modified Dockerfile for unnamed stage")
	}
	if !stringContains(modified, "AS myname") {
		t.Errorf("modified should contain 'AS myname'")
	}
}

func TestFindBaseImage_CycleProtection(t *testing.T) {
	// Stage references itself (cycle)
	dockerfile := `FROM stage1 as stage1
RUN echo hello
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "stage1")
	// Should not hang — returns empty on cycle
	if image != "" {
		t.Errorf("expected empty for cycle, got %q", image)
	}
}

func TestBuildArgs_ExtraArgs(t *testing.T) {
	c := NewClient("docker", nil, nil)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
		ExtraArgs:   []string{"--build-context", "features=./features"},
	})

	found := false
	for i, a := range args {
		if a == "--build-context" && i+1 < len(args) && args[i+1] == "features=./features" {
			found = true
		}
	}
	if !found {
		t.Error("extra args not passed through")
	}
}
