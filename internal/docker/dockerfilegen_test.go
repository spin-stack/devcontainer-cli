package docker

import (
	"strings"
	"testing"
)

func envBuild(key, value string) func(*DockerfileBuilder) {
	return func(b *DockerfileBuilder) { b.Env(key, value) }
}

func TestDockerfileBuilder(t *testing.T) {
	tests := []struct {
		name         string
		build        func(b *DockerfileBuilder)
		wantContains []string
	}{
		{
			name: "basic directives",
			build: func(b *DockerfileBuilder) {
				b.Arg("BASE", "ubuntu:22.04")
				b.From("$BASE").As("builder")
				b.User("root")
				b.Env("FOO", "bar")
				b.Run("apt-get update")
			},
			wantContains: []string{
				"ARG BASE=ubuntu:22.04", "FROM $BASE AS builder", "USER root",
				`ENV FOO="bar"`, "RUN apt-get update",
			},
		},
		{name: "env escaping: simple", build: envBuild("SIMPLE", "hello"), wantContains: []string{`ENV SIMPLE="hello"`}},
		{name: "env escaping: dollar", build: envBuild("DOLLAR", "value with $dollar"), wantContains: []string{`ENV DOLLAR="value with \$dollar"`}},
		{name: "env escaping: backslash", build: envBuild("BACKSLASH", `value with \back`), wantContains: []string{`ENV BACKSLASH="value with \\back"`}},
		{name: "env escaping: quotes", build: envBuild("QUOTES", `value with "quotes"`), wantContains: []string{`ENV QUOTES="value with \"quotes\""`}},
		{name: "env escaping: all", build: envBuild("ALL", `$1 "2" \3`), wantContains: []string{`ENV ALL="\$1 \"2\" \\3"`}},
		{
			name: "label escaping",
			build: func(b *DockerfileBuilder) {
				b.Label("devcontainer.metadata", `[{"remoteUser":"node","postCreateCommand":"echo \"Val: $TEST\""}]`)
			},
			wantContains: []string{`LABEL devcontainer.metadata="[{\"remoteUser\":\"node\",\"postCreateCommand\":\"echo \\\"Val: \$TEST\\\"\"}]"`},
		},
		{
			name:         "copy from stage",
			build:        func(b *DockerfileBuilder) { b.Copy("src/", "/dst/").From("builder") },
			wantContains: []string{"COPY --from=builder src/ /dst/"},
		},
		{
			name:         "from with platform",
			build:        func(b *DockerfileBuilder) { b.From("ubuntu:22.04").Platform("linux/amd64").As("base") },
			wantContains: []string{"FROM --platform=linux/amd64 ubuntu:22.04 AS base"},
		},
		{
			name:         "raw env",
			build:        func(b *DockerfileBuilder) { b.EnvRaw("_BUILD_ARG_GO=true"); b.EnvRaw(`GOPATH="/go"`) },
			wantContains: []string{"ENV _BUILD_ARG_GO=true", `ENV GOPATH="/go"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewDockerfileBuilder()
			tt.build(b)
			out := b.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}
