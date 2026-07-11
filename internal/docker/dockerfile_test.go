package docker

import (
	"testing"
)

// --- EnsureDockerfileHasFinalStageName ---

func TestEnsureFinalStageName_Named(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base as final

COPY src dest
RUN another command
`
	name, modified := EnsureDockerfileHasFinalStageName(dockerfile, "placeholder")
	if name != "final" {
		t.Errorf("name = %q, want 'final'", name)
	}
	if modified != "" {
		t.Error("should not modify when already named")
	}
}

func TestEnsureFinalStageName_Unnamed(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base

COPY src dest
RUN another command
`
	name, modified := EnsureDockerfileHasFinalStageName(dockerfile, "placeholder")
	if name != "placeholder" {
		t.Errorf("name = %q, want 'placeholder'", name)
	}
	if modified == "" {
		t.Fatal("expected modification")
	}
	if !contains(modified, "FROM base AS placeholder") {
		t.Errorf("modified should contain 'FROM base AS placeholder', got:\n%s", modified)
	}
}

func TestEnsureFinalStageName_TrailingFROM(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base`
	name, modified := EnsureDockerfileHasFinalStageName(dockerfile, "placeholder")
	if name != "placeholder" {
		t.Errorf("name = %q", name)
	}
	if modified == "" {
		t.Fatal("expected modification")
	}
	if !contains(modified, "FROM base AS placeholder") {
		t.Errorf("modified = %q", modified)
	}
}

func TestEnsureFinalStageName_WithPlatform(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

 	FROM  --platform=my-platform 	base   #<- deliberately mixing with whitespace

COPY src dest
RUN another command
`
	name, modified := EnsureDockerfileHasFinalStageName(dockerfile, "placeholder")
	if name != "placeholder" {
		t.Errorf("name = %q", name)
	}
	if modified == "" {
		t.Fatal("expected modification")
	}
	if !contains(modified, "AS placeholder") {
		t.Errorf("modified should contain 'AS placeholder'")
	}
}

// --- FindBaseImage ---

func TestFindBaseImage_Simple(t *testing.T) {
	df := ExtractDockerfile("FROM image1\nUSER user1\n")
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "image1" {
		t.Errorf("image = %q", image)
	}
}

func TestFindBaseImage_Arg(t *testing.T) {
	dockerfile := `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "image2" {
		t.Errorf("image = %q, want image2", image)
	}
}

func TestFindBaseImage_ArgOverridden(t *testing.T) {
	dockerfile := `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{"BASE_IMAGE": "image3"}, "")
	if image != "image3" {
		t.Errorf("image = %q, want image3", image)
	}
}

func TestFindBaseImage_Multistage(t *testing.T) {
	dockerfile := `
FROM image1 as stage1
FROM stage3 as stage2
FROM image3 as stage3
FROM image4 as stage4
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "stage2")
	if image != "image3" {
		t.Errorf("image = %q, want image3", image)
	}
}

func TestFindBaseImage_Quoted(t *testing.T) {
	dockerfile := `
ARG BASE_IMAGE="ubuntu:latest"

FROM "${BASE_IMAGE}"
`
	df := ExtractDockerfile(dockerfile)
	if len(df.Stages) != 1 {
		t.Fatalf("stages = %d", len(df.Stages))
	}
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "ubuntu:latest" {
		t.Errorf("image = %q, want ubuntu:latest", image)
	}
}

func TestFindBaseImage_VarExpPositive_Set(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{"cloud": "true"}, "")
	if image != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("image = %q", image)
	}
}

func TestFindBaseImage_VarExpPositive_Unset(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "azure-cli:latest" {
		t.Errorf("image = %q", image)
	}
}

func TestFindBaseImage_VarExpNegative_Set(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{"cloud": "ghcr.io/"}, "")
	if image != "ghcr.io/azure-cli:latest" {
		t.Errorf("image = %q", image)
	}
}

func TestFindBaseImage_VarExpNegative_Unset(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`
	df := ExtractDockerfile(dockerfile)
	image := FindBaseImage(df, map[string]string{}, "")
	if image != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("image = %q", image)
	}
}

// --- FindUserStatement ---

func TestFindUser_Simple(t *testing.T) {
	df := ExtractDockerfile("FROM debian\nUSER user1\n")
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "")
	if user != "user1" {
		t.Errorf("user = %q", user)
	}
}

func TestFindUser_Arg(t *testing.T) {
	dockerfile := `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	df := ExtractDockerfile(dockerfile)
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "")
	if user != "user2" {
		t.Errorf("user = %q, want user2", user)
	}
}

func TestFindUser_ArgOverridden(t *testing.T) {
	dockerfile := `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	df := ExtractDockerfile(dockerfile)
	user := FindUserStatement(df, map[string]string{"IMAGE_USER": "user3"}, map[string]string{}, "")
	if user != "user3" {
		t.Errorf("user = %q, want user3", user)
	}
}

func TestFindUser_Multistage(t *testing.T) {
	dockerfile := `
FROM image1 as stage1
USER user1
FROM stage3 as stage2
FROM image3 as stage3
USER user3_1
USER user3_2
FROM image4 as stage4
USER user4
`
	df := ExtractDockerfile(dockerfile)
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "stage2")
	if user != "user3_2" {
		t.Errorf("user = %q, want user3_2", user)
	}
}

func TestFindUser_NoUser(t *testing.T) {
	df := ExtractDockerfile("FROM debian\nRUN echo hello\n")
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "")
	if user != "" {
		t.Errorf("user = %q, want empty", user)
	}
}

func TestFindUser_LastStageDefault(t *testing.T) {
	dockerfile := `
FROM image1 as stage1
USER user1
FROM image2 as stage2
USER user2
`
	df := ExtractDockerfile(dockerfile)
	user := FindUserStatement(df, map[string]string{}, map[string]string{}, "")
	if user != "user2" {
		t.Errorf("user = %q, want user2 (last stage)", user)
	}
}

// --- SupportsBuildContexts ---

func TestSupportsBuildContexts_NoSyntax(t *testing.T) {
	df := ExtractDockerfile("FROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if ok || unknown {
		t.Errorf("ok=%v unknown=%v", ok, unknown)
	}
}

func TestSupportsBuildContexts_V14(t *testing.T) {
	df := ExtractDockerfile("# syntax=docker/dockerfile:1.4\nFROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if !ok || unknown {
		t.Errorf("ok=%v unknown=%v (1.4 should support)", ok, unknown)
	}
}

func TestSupportsBuildContexts_V13(t *testing.T) {
	df := ExtractDockerfile("# syntax=docker/dockerfile:1.3\nFROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if ok || unknown {
		t.Errorf("ok=%v unknown=%v (1.3 should not support)", ok, unknown)
	}
}

func TestSupportsBuildContexts_Latest(t *testing.T) {
	df := ExtractDockerfile("# syntax=docker/dockerfile\nFROM ubuntu\n")
	ok, unknown := SupportsBuildContexts(df)
	if !ok || unknown {
		t.Errorf("ok=%v unknown=%v (no tag → latest → should support)", ok, unknown)
	}
}

// --- ExtractDockerfile ---

func TestExtractDockerfile_MultistageCount(t *testing.T) {
	dockerfile := `FROM debian:latest as base
FROM ubuntu:latest as dev
`
	df := ExtractDockerfile(dockerfile)
	if len(df.Stages) != 2 {
		t.Errorf("stages = %d, want 2", len(df.Stages))
	}
	if df.Stages[0].From.Image != "debian:latest" {
		t.Errorf("stage0 image = %q", df.Stages[0].From.Image)
	}
	if df.Stages[1].From.Image != "ubuntu:latest" {
		t.Errorf("stage1 image = %q", df.Stages[1].From.Image)
	}
}

func TestExtractDockerfile_WithPreamble(t *testing.T) {
	dockerfile := `# syntax=docker/dockerfile:1.4
ARG BASE=ubuntu
FROM ${BASE}
`
	df := ExtractDockerfile(dockerfile)
	if df.Preamble.Version != "1.4" {
		t.Errorf("version = %q, want 1.4", df.Preamble.Version)
	}
	if len(df.Preamble.Instructions) != 1 {
		t.Errorf("preamble instructions = %d", len(df.Preamble.Instructions))
	}
	if df.Preamble.Instructions[0].Name != "BASE" {
		t.Errorf("preamble arg name = %q", df.Preamble.Instructions[0].Name)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
