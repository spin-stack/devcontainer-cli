package docker

import (
	"strings"
	"testing"
)

func TestDockerfileBuilder_Basic(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Arg("BASE", "ubuntu:22.04")
	b.From("$BASE").As("builder")
	b.User("root")
	b.Env("FOO", "bar")
	b.Run("apt-get update")

	out := b.String()

	if !strings.Contains(out, "ARG BASE=ubuntu:22.04") {
		t.Error("missing ARG")
	}
	if !strings.Contains(out, "FROM $BASE AS builder") {
		t.Error("missing FROM AS")
	}
	if !strings.Contains(out, "USER root") {
		t.Error("missing USER")
	}
	if !strings.Contains(out, `ENV FOO="bar"`) {
		t.Error("missing ENV")
	}
	if !strings.Contains(out, "RUN apt-get update") {
		t.Error("missing RUN")
	}
}

func TestDockerfileBuilder_EnvEscaping(t *testing.T) {
	tests := []struct {
		key, value, want string
	}{
		{"SIMPLE", "hello", `ENV SIMPLE="hello"`},
		{"DOLLAR", "value with $dollar", `ENV DOLLAR="value with \$dollar"`},
		{"BACKSLASH", `value with \back`, `ENV BACKSLASH="value with \\back"`},
		{"QUOTES", `value with "quotes"`, `ENV QUOTES="value with \"quotes\""`},
		{"ALL", `$1 "2" \3`, `ENV ALL="\$1 \"2\" \\3"`},
	}

	for _, tt := range tests {
		b := NewDockerfileBuilder()
		b.Env(tt.key, tt.value)
		out := b.String()
		if !strings.Contains(out, tt.want) {
			t.Errorf("Env(%q, %q) = %q, want line containing %q", tt.key, tt.value, out, tt.want)
		}
	}
}

func TestDockerfileBuilder_LabelEscaping(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Label("devcontainer.metadata", `[{"remoteUser":"node","postCreateCommand":"echo \"Val: $TEST\""}]`)

	out := b.String()
	// Should have escaped $, " and \
	if !strings.Contains(out, `LABEL devcontainer.metadata="[{\"remoteUser\":\"node\",\"postCreateCommand\":\"echo \\\"Val: \$TEST\\\"\"}]"`) {
		t.Errorf("unexpected label output: %s", out)
	}
}

func TestDockerfileBuilder_CopyFrom(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Copy("src/", "/dst/").From("builder")

	out := b.String()
	if !strings.Contains(out, "COPY --from=builder src/ /dst/") {
		t.Errorf("unexpected COPY: %s", out)
	}
}

func TestDockerfileBuilder_FromPlatform(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("ubuntu:22.04").Platform("linux/amd64").As("base")

	out := b.String()
	if !strings.Contains(out, "FROM --platform=linux/amd64 ubuntu:22.04 AS base") {
		t.Errorf("unexpected FROM: %s", out)
	}
}

func TestDockerfileBuilder_EnvRaw(t *testing.T) {
	b := NewDockerfileBuilder()
	b.EnvRaw("_BUILD_ARG_GO=true")
	b.EnvRaw(`GOPATH="/go"`)

	out := b.String()
	if !strings.Contains(out, "ENV _BUILD_ARG_GO=true") {
		t.Error("missing raw env 1")
	}
	if !strings.Contains(out, `ENV GOPATH="/go"`) {
		t.Error("missing raw env 2")
	}
}
