package docker

import (
	"testing"
)

// --- EnsureDockerfileHasFinalStageName ---

func TestEnsureFinalStageName(t *testing.T) {
	tests := []struct {
		name         string
		dockerfile   string
		placeholder  string
		wantName     string
		wantModified bool   // true: modified must be non-empty; false: must be ""
		wantContains string // substring modified must contain ("" = skip)
	}{
		{
			name: "named",
			dockerfile: `
FROM ubuntu:latest as base

RUN some command

FROM base as final

COPY src dest
RUN another command
`,
			placeholder:  "placeholder",
			wantName:     "final",
			wantModified: false,
		},
		{
			name: "unnamed",
			dockerfile: `
FROM ubuntu:latest as base

RUN some command

FROM base

COPY src dest
RUN another command
`,
			placeholder:  "placeholder",
			wantName:     "placeholder",
			wantModified: true,
			wantContains: "FROM base AS placeholder",
		},
		{
			name: "trailing_from",
			dockerfile: `
FROM ubuntu:latest as base

RUN some command

FROM base`,
			placeholder:  "placeholder",
			wantName:     "placeholder",
			wantModified: true,
			wantContains: "FROM base AS placeholder",
		},
		{
			name: "with_platform",
			dockerfile: `
FROM ubuntu:latest as base

RUN some command

 	FROM  --platform=my-platform 	base   #<- deliberately mixing with whitespace

COPY src dest
RUN another command
`,
			placeholder:  "placeholder",
			wantName:     "placeholder",
			wantModified: true,
			wantContains: "AS placeholder",
		},
		{
			name:         "single_stage",
			dockerfile:   "FROM ubuntu:latest\nRUN echo hello\n",
			placeholder:  "myname",
			wantName:     "myname",
			wantModified: true,
			wantContains: "AS myname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, modified := EnsureDockerfileHasFinalStageName(tt.dockerfile, tt.placeholder)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if tt.wantModified {
				if modified == "" {
					t.Fatal("expected modification")
				}
				if tt.wantContains != "" && !contains(modified, tt.wantContains) {
					t.Errorf("modified should contain %q, got:\n%s", tt.wantContains, modified)
				}
			} else if modified != "" {
				t.Error("should not modify when already named")
			}
		})
	}
}

// --- FindBaseImage ---

func TestFindBaseImage(t *testing.T) {
	tests := []struct {
		name         string
		dockerfile   string
		args         map[string]string
		stage        string
		want         string
		wantStages   int    // 0 = skip stage-count check
		wantPlatform string // "" = skip; checks Stages[0].From.Platform
	}{
		{
			name:       "simple",
			dockerfile: "FROM image1\nUSER user1\n",
			want:       "image1",
		},
		{
			name: "arg",
			dockerfile: `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
ARG IMAGE_USER=user2
USER $IMAGE_USER
`,
			want: "image2",
		},
		{
			name: "arg_overridden",
			dockerfile: `ARG BASE_IMAGE="image2"
FROM ${BASE_IMAGE}
`,
			args: map[string]string{"BASE_IMAGE": "image3"},
			want: "image3",
		},
		{
			name: "multistage",
			dockerfile: `
FROM image1 as stage1
FROM stage3 as stage2
FROM image3 as stage3
FROM image4 as stage4
`,
			stage: "stage2",
			want:  "image3",
		},
		{
			name: "quoted",
			dockerfile: `
ARG BASE_IMAGE="ubuntu:latest"

FROM "${BASE_IMAGE}"
`,
			want:       "ubuntu:latest",
			wantStages: 1,
		},
		{
			name: "varexp_positive_set",
			dockerfile: `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`,
			args: map[string]string{"cloud": "true"},
			want: "mcr.microsoft.com/azure-cli:latest",
		},
		{
			name: "varexp_positive_unset",
			dockerfile: `
ARG cloud
FROM ${cloud:+mcr.microsoft.com/}azure-cli:latest
`,
			want: "azure-cli:latest",
		},
		{
			name: "varexp_negative_set",
			dockerfile: `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`,
			args: map[string]string{"cloud": "ghcr.io/"},
			want: "ghcr.io/azure-cli:latest",
		},
		{
			name: "varexp_negative_unset",
			dockerfile: `
ARG cloud
FROM ${cloud:-mcr.microsoft.com/}azure-cli:latest
`,
			want: "mcr.microsoft.com/azure-cli:latest",
		},
		{
			name: "with_platform",
			dockerfile: `FROM --platform=linux/amd64 ubuntu:22.04
RUN echo hello
`,
			want:         "ubuntu:22.04",
			wantPlatform: "linux/amd64",
		},
		{
			name: "cycle_protection",
			dockerfile: `FROM stage1 as stage1
RUN echo hello
`,
			stage: "stage1",
			want:  "", // returns empty on cycle, must not hang
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			df := ExtractDockerfile(tt.dockerfile)
			if tt.wantStages != 0 && len(df.Stages) != tt.wantStages {
				t.Fatalf("stages = %d, want %d", len(df.Stages), tt.wantStages)
			}
			image := FindBaseImage(df, tt.args, tt.stage)
			if image != tt.want {
				t.Errorf("image = %q, want %q", image, tt.want)
			}
			if tt.wantPlatform != "" && df.Stages[0].From.Platform != tt.wantPlatform {
				t.Errorf("platform = %q, want %q", df.Stages[0].From.Platform, tt.wantPlatform)
			}
		})
	}
}

// --- FindUserStatement ---

func TestFindUser(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		args       map[string]string
		stage      string
		want       string
	}{
		{
			name:       "simple",
			dockerfile: "FROM debian\nUSER user1\n",
			want:       "user1",
		},
		{
			name: "arg",
			dockerfile: `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`,
			want: "user2",
		},
		{
			name: "arg_overridden",
			dockerfile: `FROM debian
ARG IMAGE_USER=user2
USER $IMAGE_USER
`,
			args: map[string]string{"IMAGE_USER": "user3"},
			want: "user3",
		},
		{
			name: "multistage",
			dockerfile: `
FROM image1 as stage1
USER user1
FROM stage3 as stage2
FROM image3 as stage3
USER user3_1
USER user3_2
FROM image4 as stage4
USER user4
`,
			stage: "stage2",
			want:  "user3_2",
		},
		{
			name:       "no_user",
			dockerfile: "FROM debian\nRUN echo hello\n",
			want:       "",
		},
		{
			name: "last_stage_default",
			dockerfile: `
FROM image1 as stage1
USER user1
FROM image2 as stage2
USER user2
`,
			want: "user2",
		},
		{
			name: "multiple_users",
			dockerfile: `FROM debian
USER first
USER second
USER third
`,
			want: "third", // last USER wins
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			df := ExtractDockerfile(tt.dockerfile)
			user := FindUserStatement(df, tt.args, map[string]string{}, tt.stage)
			if user != tt.want {
				t.Errorf("user = %q, want %q", user, tt.want)
			}
		})
	}
}

// --- SupportsBuildContexts ---

func TestSupportsBuildContexts(t *testing.T) {
	tests := []struct {
		name        string
		dockerfile  string
		wantOK      bool
		wantUnknown bool
	}{
		{"no_syntax", "FROM ubuntu\n", false, false},
		{"v2", "# syntax=docker/dockerfile:2\nFROM ubuntu\n", true, false},
		{"labs", "# syntax=docker/dockerfile:labs\nFROM ubuntu\n", true, false},
		{"v14", "# syntax=docker/dockerfile:1.4\nFROM ubuntu\n", true, false},
		{"v13", "# syntax=docker/dockerfile:1.3\nFROM ubuntu\n", false, false},
		{"latest", "# syntax=docker/dockerfile\nFROM ubuntu\n", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			df := ExtractDockerfile(tt.dockerfile)
			ok, unknown := SupportsBuildContexts(df)
			if ok != tt.wantOK || unknown != tt.wantUnknown {
				t.Errorf("ok=%v unknown=%v, want ok=%v unknown=%v", ok, unknown, tt.wantOK, tt.wantUnknown)
			}
		})
	}
}

// --- ExtractDockerfile ---

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
