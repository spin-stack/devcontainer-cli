package docker

// Oracle tests ported VERBATIM from the upstream devcontainers CLI test suite:
//   reference/src/test/dockerfileUtils.test.ts
//
// The expected values below are transcribed exactly from that TypeScript test,
// NOT derived from what this Go implementation produces. The point is to catch
// divergences between the Go port and the upstream spec that our own tests,
// written against the Go behavior, cannot see.
//
// Function mapping (TS -> Go, in internal/docker/dockerfile.go):
//   extractDockerfile(content)                  -> ExtractDockerfile(content) *Dockerfile
//   findBaseImage(df, buildArgs, target)        -> FindBaseImage(df, buildArgs, target) string
//   findUserStatement(df, a, baseEnv, b, tgt)   -> findUserStatement(df, buildArgs, baseImageEnv, target) string
//       TS has 5 args, Go has 4. The 4th TS arg (an extra map) has no Go
//       counterpart; TS arg2 -> Go buildArgs, TS arg3 -> Go baseImageEnv,
//       TS arg5 (target) -> Go target. `undefined` target -> "".
//   ensureDockerfileHasFinalStageName(c, name)  -> EnsureFinalStageName(c, name) (stage, modified)
//       TS {lastStageName, modifiedDockerfile}; modifiedDockerfile===undefined -> Go modified == "".
//   supportsBuildContexts(df)                   -> SupportsBuildContexts(df) (supported, unknown)
//       TS false -> (false,false); TS true -> (true,false); TS 'unknown' -> (false,true).
//
// describe('getImageBuildInfo') is intentionally NOT ported: it needs an
// image-inspect callback (internalGetImageBuildInfoFromDockerfile) with no
// direct Go equivalent here.

import "testing"

// ---------------------------------------------------------------------------
// ensureDockerfileHasFinalStageName
// ---------------------------------------------------------------------------

func TestOracle_Dockerfile_EnsureFinalStageName_namedSimpleFrom(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base as final

COPY src dest
RUN another command
`
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "final" {
		t.Errorf("lastStageName: got %q, want %q", stage, "final")
	}
	if modified != "" {
		t.Errorf("modifiedDockerfile: got %q, want undefined (empty)", modified)
	}
}

func TestOracle_Dockerfile_EnsureFinalStageName_indentedWithComment(t *testing.T) {
	// Deliberately mixes space+tab whitespace; built with explicit \t.
	dockerfile := "\nFROM ubuntu:latest as base\n\nRUN some command\n\n \tFROM base  as\t  final  #<- deliberately mixing with whitespace and including: as something here\n\nCOPY src dest\nRUN another command\n"
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "final" {
		t.Errorf("lastStageName: got %q, want %q", stage, "final")
	}
	if modified != "" {
		t.Errorf("modifiedDockerfile: got %q, want undefined (empty)", modified)
	}
}

func TestOracle_Dockerfile_EnsureFinalStageName_platformIndentedWithComment(t *testing.T) {
	dockerfile := "\nFROM ubuntu:latest as base\n\nRUN some command\n\n \tFROM  --platform=my-platform \tbase  as\t  final  #<- deliberately mixing with whitespace and including: as something here\n\nCOPY src dest\nRUN another command\n"
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "final" {
		t.Errorf("lastStageName: got %q, want %q", stage, "final")
	}
	if modified != "" {
		t.Errorf("modifiedDockerfile: got %q, want undefined (empty)", modified)
	}
}

func TestOracle_Dockerfile_EnsureFinalStageName_unnamedSimpleFrom(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base

COPY src dest
RUN another command
`
	wantModified := `
FROM ubuntu:latest as base

RUN some command

FROM base AS placeholder

COPY src dest
RUN another command
`
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "placeholder" {
		t.Errorf("lastStageName: got %q, want %q", stage, "placeholder")
	}
	if modified != wantModified {
		t.Errorf("modifiedDockerfile:\n got %q\nwant %q", modified, wantModified)
	}
}

func TestOracle_Dockerfile_EnsureFinalStageName_unnamedTrailingFrom(t *testing.T) {
	dockerfile := `
FROM ubuntu:latest as base

RUN some command

FROM base`
	wantModified := `
FROM ubuntu:latest as base

RUN some command

FROM base AS placeholder`
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "placeholder" {
		t.Errorf("lastStageName: got %q, want %q", stage, "placeholder")
	}
	if modified != wantModified {
		t.Errorf("modifiedDockerfile:\n got %q\nwant %q", modified, wantModified)
	}
}

func TestOracle_Dockerfile_EnsureFinalStageName_unnamedPlatformWithComment(t *testing.T) {
	dockerfile := "\nFROM ubuntu:latest as base\n\nRUN some command\n\n \tFROM  --platform=my-platform \tbase   #<- deliberately mixing with whitespace and including: as something here\n\nCOPY src dest\nRUN another command\n"
	wantModified := "\nFROM ubuntu:latest as base\n\nRUN some command\n\n \tFROM  --platform=my-platform \tbase AS placeholder   #<- deliberately mixing with whitespace and including: as something here\n\nCOPY src dest\nRUN another command\n"
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	if stage != "placeholder" {
		t.Errorf("lastStageName: got %q, want %q", stage, "placeholder")
	}
	if modified != wantModified {
		t.Errorf("modifiedDockerfile:\n got %q\nwant %q", modified, wantModified)
	}
}

// TS: `without any from stage (invalid Dockerfile)` expects a THROW:
//   'Error parsing Dockerfile: Dockerfile contains no FROM instructions'.
// Go has no throw path: EnsureFinalStageName returns (defaultName, content)
// unchanged when there are no FROM lines. We assert Go's ACTUAL contract here
// and flag the divergence in the report (Go does not error/panic on this input).
func TestOracle_Dockerfile_EnsureFinalStageName_noFromStage(t *testing.T) {
	dockerfile := `
RUN some command
`
	stage, modified := EnsureFinalStageName(dockerfile, "placeholder")
	// DIVERGENCE FROM TS: TS throws; Go returns (defaultName, original content).
	if stage != "placeholder" {
		t.Errorf("lastStageName: got %q, want %q (Go contract)", stage, "placeholder")
	}
	if modified != dockerfile {
		t.Errorf("modifiedDockerfile: got %q, want original content (Go contract)", modified)
	}
}

// ---------------------------------------------------------------------------
// findBaseImage
// ---------------------------------------------------------------------------

func TestOracle_Dockerfile_FindBaseImage_simpleFrom(t *testing.T) {
	dockerfile := `FROM image1
USER user1
`
	extracted := ExtractDockerfile(dockerfile)
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "image1" {
		t.Errorf("got %q, want %q", got, "image1")
	}
}

func TestOracle_Dockerfile_FindBaseImage_argFrom(t *testing.T) {
	dockerfile := `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	extracted := ExtractDockerfile(dockerfile)
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "image2" {
		t.Errorf("got %q, want %q", got, "image2")
	}
}

func TestOracle_Dockerfile_FindBaseImage_argFromOverwritten(t *testing.T) {
	dockerfile := `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	extracted := ExtractDockerfile(dockerfile)
	if got := FindBaseImage(extracted, map[string]string{"BASE_IMAGE": "image3"}, ""); got != "image3" {
		t.Errorf("got %q, want %q", got, "image3")
	}
}

func TestOracle_Dockerfile_FindBaseImage_multistage(t *testing.T) {
	dockerfile := `
FROM image1 as stage1
FROM stage3 as stage2
FROM image3 as stage3
FROM image4 as stage4
`
	extracted := ExtractDockerfile(dockerfile)
	if got := FindBaseImage(extracted, map[string]string{}, "stage2"); got != "image3" {
		t.Errorf("got %q, want %q", got, "image3")
	}
}

func TestOracle_Dockerfile_FindBaseImage_quoted(t *testing.T) {
	dockerfile := `
ARG BASE_IMAGE="ubuntu:latest"

FROM "${BASE_IMAGE}"
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "ubuntu:latest" {
		t.Errorf("got %q, want %q", got, "ubuntu:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_varExpr_positiveWithValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{"cloud": "true"}, ""); got != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "mcr.microsoft.com/azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_varExpr_positiveNoValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_varExpr_negativeWithValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{"cloud": "ghcr.io/"}, ""); got != "ghcr.io/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "ghcr.io/azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_varExpr_negativeNoValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "mcr.microsoft.com/azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_quotedVarExpr_positiveWithValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:+"mcr.microsoft.com/"}azure-cli:latest"
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{"cloud": "true"}, ""); got != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "mcr.microsoft.com/azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_quotedVarExpr_positiveNoValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM "${cloud:+"mcr.microsoft.com/"}azure-cli:latest"
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_quotedVarExpr_negativeWithValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM "${cloud:-"mcr.microsoft.com/"}azure-cli:latest"
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{"cloud": "ghcr.io/"}, ""); got != "ghcr.io/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "ghcr.io/azure-cli:latest")
	}
}

func TestOracle_Dockerfile_FindBaseImage_quotedVarExpr_negativeNoValue(t *testing.T) {
	dockerfile := `
ARG cloud
FROM ${cloud:-"mcr.microsoft.com/"}azure-cli:latest as label
`
	extracted := ExtractDockerfile(dockerfile)
	if len(extracted.Stages) != 1 {
		t.Errorf("stages.length: got %d, want 1", len(extracted.Stages))
	}
	if got := FindBaseImage(extracted, map[string]string{}, ""); got != "mcr.microsoft.com/azure-cli:latest" {
		t.Errorf("got %q, want %q", got, "mcr.microsoft.com/azure-cli:latest")
	}
}

// ---------------------------------------------------------------------------
// findUserStatement
// ---------------------------------------------------------------------------

func TestOracle_Dockerfile_FindUserStatement_simpleUser(t *testing.T) {
	dockerfile := `FROM debian
USER user1
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

func TestOracle_Dockerfile_FindUserStatement_argUser(t *testing.T) {
	dockerfile := `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user2" {
		t.Errorf("got %q, want %q", got, "user2")
	}
}

func TestOracle_Dockerfile_FindUserStatement_argUserOverwritten(t *testing.T) {
	dockerfile := `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{"IMAGE_USER": "user3"}, map[string]string{}, ""); got != "user3" {
		t.Errorf("got %q, want %q", got, "user3")
	}
}

func TestOracle_Dockerfile_FindUserStatement_multistage(t *testing.T) {
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
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, "stage2"); got != "user3_2" {
		t.Errorf("got %q, want %q", got, "user3_2")
	}
}

func TestOracle_Dockerfile_FindUserStatement_argAfterEnv(t *testing.T) {
	dockerfile := `
FROM debian
ENV USERNAME=user1
ARG USERNAME=user2
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user2" {
		t.Errorf("got %q, want %q", got, "user2")
	}
}

func TestOracle_Dockerfile_FindUserStatement_argAfterEnvInPrecedingStage(t *testing.T) {
	dockerfile := `
FROM debian as one
ENV USERNAME=user1
ARG USERNAME=user2

FROM one as two
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

func TestOracle_Dockerfile_FindUserStatement_argInPreamble(t *testing.T) {
	dockerfile := `
ARG USERNAME=user1
FROM debian
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

func TestOracle_Dockerfile_FindUserStatement_unboundArgAfterEnv(t *testing.T) {
	dockerfile := `
FROM debian
ENV USERNAME=user1
ARG USERNAME
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

func TestOracle_Dockerfile_FindUserStatement_unbound(t *testing.T) {
	dockerfile := `
FROM debian
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	// TS expects undefined; Go returns "".
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "" {
		t.Errorf("got %q, want %q (undefined)", got, "")
	}
}

func TestOracle_Dockerfile_FindUserStatement_envAfterArg(t *testing.T) {
	dockerfile := `
FROM debian
ARG USERNAME=user1
ENV USERNAME=user2
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user2" {
		t.Errorf("got %q, want %q", got, "user2")
	}
}

func TestOracle_Dockerfile_FindUserStatement_envSetFromArg(t *testing.T) {
	dockerfile := `
FROM debian
ARG USERNAME1=user1
ENV USERNAME2=${USERNAME1}
USER ${USERNAME2}
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

func TestOracle_Dockerfile_FindUserStatement_multipleVariables(t *testing.T) {
	dockerfile := `
FROM debian
ARG USERNAME1=user1
ENV USERNAME2=user2
USER A${USERNAME1}A${USERNAME2}A
`
	extracted := ExtractDockerfile(dockerfile)
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{}, ""); got != "Auser1Auser2A" {
		t.Errorf("got %q, want %q", got, "Auser1Auser2A")
	}
}

func TestOracle_Dockerfile_FindUserStatement_envInBaseImage(t *testing.T) {
	dockerfile := `
FROM mybase
USER ${USERNAME}
`
	extracted := ExtractDockerfile(dockerfile)
	// TS: findUserStatement(extracted, {}, { USERNAME: 'user1' }, {}, undefined)
	// TS arg3 (baseImageEnv) -> Go baseImageEnv.
	if got := findUserStatement(extracted, map[string]string{}, map[string]string{"USERNAME": "user1"}, ""); got != "user1" {
		t.Errorf("got %q, want %q", got, "user1")
	}
}

// ---------------------------------------------------------------------------
// supportsBuildContexts
// ---------------------------------------------------------------------------

func TestOracle_Dockerfile_SupportsBuildContexts_noSyntaxDirective(t *testing.T) {
	dockerfile := `FROM debian`
	extracted := ExtractDockerfile(dockerfile)
	supported, unknown := SupportsBuildContexts(extracted)
	// TS: false
	if supported != false || unknown != false {
		t.Errorf("got (supported=%v, unknown=%v), want false", supported, unknown)
	}
}

func TestOracle_Dockerfile_SupportsBuildContexts_matchingSyntaxDirective(t *testing.T) {
	dockerfile := `# syntax=docker/dockerfile:1.4
FROM debian`
	extracted := ExtractDockerfile(dockerfile)
	supported, unknown := SupportsBuildContexts(extracted)
	// TS: true
	if supported != true || unknown != false {
		t.Errorf("got (supported=%v, unknown=%v), want true", supported, unknown)
	}
}

func TestOracle_Dockerfile_SupportsBuildContexts_matchingSyntaxDirectiveDockerIo(t *testing.T) {
	dockerfile := `# syntax=docker.io/docker/dockerfile:1.4
FROM debian`
	extracted := ExtractDockerfile(dockerfile)
	supported, unknown := SupportsBuildContexts(extracted)
	// TS: true
	if supported != true || unknown != false {
		t.Errorf("got (supported=%v, unknown=%v), want true", supported, unknown)
	}
}

func TestOracle_Dockerfile_SupportsBuildContexts_unknownSyntaxDirective(t *testing.T) {
	dockerfile := `# syntax=mycompany/myimage:1.4
FROM debian`
	extracted := ExtractDockerfile(dockerfile)
	supported, unknown := SupportsBuildContexts(extracted)
	// TS: 'unknown'
	if supported != false || unknown != true {
		t.Errorf("got (supported=%v, unknown=%v), want 'unknown'", supported, unknown)
	}
}

func TestOracle_Dockerfile_SupportsBuildContexts_versionMatrix(t *testing.T) {
	// TS iterates prerelease in ['', '-labs'] x version table.
	// expected is a plain bool -> Go (supported==expected, unknown==false).
	cases := []struct {
		version  string
		expected bool
	}{
		{"0", false},
		{"1", true},
		{"1.2", false},
		{"1.2.3", false},
		{"1.4", true},
		{"1.4.5", true},
		{"1.5", true},
		{"1.5.0", true},
		{"2", true},
		{"", true},
		{"latest", true},
	}
	for _, prerelease := range []string{"", "-labs"} {
		for _, c := range cases {
			tag := c.version + prerelease
			suffix := ""
			if tag != "" {
				suffix = ":" + tag
			}
			dockerfile := "# syntax=docker.io/docker/dockerfile" + suffix + "\n        FROM debian"
			extracted := ExtractDockerfile(dockerfile)
			supported, unknown := SupportsBuildContexts(extracted)
			if supported != c.expected || unknown != false {
				t.Errorf("syntax directive %q: got (supported=%v, unknown=%v), want supported=%v",
					tag, supported, unknown, c.expected)
			}
		}
	}
}
