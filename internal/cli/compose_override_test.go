package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
)

// The expected strings below are the output of the TS
// generateFeaturesComposeOverrideContent template (dockerCompose.ts) for the
// same inputs, rendered with node from a verbatim copy of the template.
func TestGenerateComposeFeaturesOverrideMatchesTS(t *testing.T) {
	boolp := func(b bool) *bool { return &b }
	tests := []struct {
		name string
		in   composeFeaturesOverride
		want string
	}{
		{
			// compose-image-without-features: no Features, yet the entrypoint is
			// wrapped and the image's entrypoint kept (the compose command is
			// left alone, so no command: line).
			name: "image without features",
			in: composeFeaturesOverride{
				VersionPrefix:     "version: '3.8'\n\n",
				Service:           "app",
				UpdatedImageName:  "mcr.microsoft.com/vscode/devcontainers/javascript-node:0-16-bullseye",
				OriginalImageName: "mcr.microsoft.com/vscode/devcontainers/javascript-node:0-16-bullseye",
				Merged:            &imagemeta.MergedConfig{Init: boolp(false), Privileged: boolp(false)},
				ServiceCommand:    []string{"sleep", "infinity"},
				ImageEntrypoint:   []string{"docker-entrypoint.sh"},
				ImageCmd:          []string{"node"},
				Labels:            []string{"devcontainer.local_folder=/ws", `devcontainer.metadata=[{"remoteUser":"node"}]`},
			},
			want: "version: '3.8'\n\nservices:\n  'app':\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\ntrap \\\"exit 0\\\" 15\\n\n\\n\nexec \\\"$$@\\\"\\n\nwhile sleep 1 & wait $$!; do :; done\", \"-\", \"docker-entrypoint.sh\"]\n    labels:\n      - 'devcontainer.local_folder=/ws'\n      - 'devcontainer.metadata=[{\"remoteUser\":\"node\"}]'\n",
		},
		{
			name: "every property",
			in: composeFeaturesOverride{
				Service:           "dev",
				UpdatedImageName:  "vsc-ws-abc-uid",
				OriginalImageName: "vsc-ws-abc",
				Merged: &imagemeta.MergedConfig{
					Entrypoints:     []string{"/usr/local/share/docker-init.sh", "/usr/local/share/ssh-init.sh"},
					OverrideCommand: boolp(true),
					Init:            boolp(true),
					Privileged:      boolp(true),
					ContainerUser:   "vscode",
					ContainerEnv:    map[string]string{"A": "x$y", "B": "line1\nline2", "C": "it's"},
					CapAdd:          []string{"SYS_PTRACE", "NET_ADMIN"},
					SecurityOpt:     []string{"seccomp=unconfined"},
				},
				ServiceEntrypoint: []string{"/ep.sh"},
				ServiceCommand:    []string{"run"},
				ImageEntrypoint:   []string{"x"},
				ImageCmd:          []string{"y"},
				Labels:            []string{"devcontainer.local_folder=/ws", "x=a$b'c"},
				Mounts: []composeMount{
					{Type: "bind", Source: "/host", Target: "/c"},
					{Type: "volume", Source: "vol1", Target: "/v1"},
					{Type: "volume", Source: "ext", Target: "/v2", External: true},
					{Type: "tmpfs", Target: "/t"},
				},
				GPU: true,
			},
			want: "services:\n  'dev':\n    image: vsc-ws-abc-uid\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\ntrap \\\"exit 0\\\" 15\\n\n/usr/local/share/docker-init.sh\\n\n/usr/local/share/ssh-init.sh\\n\nexec \\\"$$@\\\"\\n\nwhile sleep 1 & wait $$!; do :; done\", \"-\"]\n    command: []\n    init: true\n    user: vscode\n    environment:\n      - 'A=x$$y'\n      - 'B=line1\\nline2'\n      - 'C=it''s'\n    privileged: true\n    cap_add:\n      - SYS_PTRACE\n      - NET_ADMIN\n    security_opt:\n      - seccomp=unconfined\n    labels:\n      - 'devcontainer.local_folder=/ws'\n      - 'x=a$$b''c'\n    volumes:\n      - /host:/c\n      - vol1:/v1\n      - ext:/v2\n      - /t\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]\nvolumes:\n  \n  vol1:\n  \n  ext:\n    external: true\n",
		},
		{
			// A compose entrypoint without a command: the image CMD is ignored and
			// an empty command is set.
			name: "compose entrypoint without command",
			in: composeFeaturesOverride{
				Service:           "app",
				UpdatedImageName:  "img",
				OriginalImageName: "img",
				Merged:            &imagemeta.MergedConfig{OverrideCommand: boolp(false)},
				ServiceEntrypoint: []string{"/ep.sh", "a$$b"},
				ImageEntrypoint:   []string{"x"},
				ImageCmd:          []string{"y"},
			},
			want: "services:\n  'app':\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\ntrap \\\"exit 0\\\" 15\\n\n\\n\nexec \\\"$$@\\\"\\n\nwhile sleep 1 & wait $$!; do :; done\", \"-\", \"/ep.sh\", \"a$$b\"]\n    command: []\n",
		},
		{
			// Neither entrypoint nor command in compose: the image's are kept,
			// with $ escaped for compose interpolation.
			name: "image entrypoint and cmd",
			in: composeFeaturesOverride{
				Service:           "app",
				UpdatedImageName:  "img",
				OriginalImageName: "img",
				Merged:            &imagemeta.MergedConfig{},
				ImageEntrypoint:   []string{"/bin/e$1"},
				ImageCmd:          []string{"echo", "$HOME"},
			},
			want: "services:\n  'app':\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\ntrap \\\"exit 0\\\" 15\\n\n\\n\nexec \\\"$$@\\\"\\n\nwhile sleep 1 & wait $$!; do :; done\", \"-\", \"/bin/e$$1\"]\n    command: [\"echo\",\"$$HOME\"]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateComposeFeaturesOverride(tt.in); got != tt.want {
				t.Errorf("override mismatch\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestGenerateComposeBuildOverride(t *testing.T) {
	tests := []struct {
		name string
		in   composeBuildOverride
		want string
	}{
		{
			name: "nothing to override",
			in:   composeBuildOverride{Service: "app"},
			want: "",
		},
		{
			// Image-only service with Features (TS buildAndExtendDockerCompose):
			// image retag, empty context, BuildKit inline cache and the Features
			// content as an additional build context.
			name: "image service with build contexts",
			in: composeBuildOverride{
				VersionPrefix:       "version: '3.8'\n\n",
				Service:             "app",
				Image:               "vsc-ws-abc",
				Dockerfile:          "/tmp/f/Dockerfile-with-features",
				Context:             "/tmp/p/empty-folder",
				BuildKitInlineCache: true,
				BuildArgs:           [][2]string{{"_DEV_CONTAINERS_BASE_IMAGE", "dev_container_auto_added_stage_label"}},
				AdditionalContexts:  [][2]string{{"dev_containers_feature_content_source", "/tmp/f"}},
			},
			want: "version: '3.8'\n\nservices:\n  app:\n    image: vsc-ws-abc\n    build:\n      dockerfile: /tmp/f/Dockerfile-with-features\n      context: /tmp/p/empty-folder\n      args:\n        - BUILDKIT_INLINE_CACHE=1\n        - _DEV_CONTAINERS_BASE_IMAGE=dev_container_auto_added_stage_label\n      additional_contexts:\n        - dev_containers_feature_content_source=/tmp/f\n\n",
		},
		{
			name: "dockerfile service with target and cache-from",
			in: composeBuildOverride{
				Service:    "app",
				Dockerfile: "/tmp/f/Dockerfile-with-features",
				Target:     "dev_containers_target_stage",
				BuildArgs:  [][2]string{{"_DEV_CONTAINERS_BASE_IMAGE", "base"}},
				CacheFrom:  []string{"a", "b"},
			},
			want: "services:\n  app:\n    build:\n      dockerfile: /tmp/f/Dockerfile-with-features\n      target: dev_containers_target_stage\n      args:\n        - _DEV_CONTAINERS_BASE_IMAGE=base\n      cache_from:\n        - a\n\n        - b\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateComposeBuildOverride(tt.in); got != tt.want {
				t.Errorf("build override mismatch\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestComposeMountsFromMetadata(t *testing.T) {
	identity := func(s string) (string, error) { return s, nil }
	got, err := composeMountsFromMetadata([]any{
		"type=volume,src=data,dst=/data",
		"source=/h,target=/c,type=bind,readonly",
		map[string]any{"type": "volume", "source": "ext", "target": "/e", "external": true},
	}, identity)
	if err != nil {
		t.Fatal(err)
	}
	want := []composeMount{
		{Type: "volume", Source: "data", Target: "/data"},
		{Type: "bind", Source: "/h", Target: "/c", ReadOnly: true},
		{Type: "volume", Source: "ext", Target: "/e", External: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d mounts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mount %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadComposeVersionPrefix(t *testing.T) {
	dir := t.TempDir()
	withVersion := filepath.Join(dir, "a.yml")
	if err := os.WriteFile(withVersion, []byte("# c\n  version: '3.8'\nservices: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	without := filepath.Join(dir, "b.yml")
	if err := os.WriteFile(without, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := readComposeVersionPrefix([]string{withVersion, without}); got != "version: '3.8'\n\n" {
		t.Errorf("got %q", got)
	}
	if got, _ := readComposeVersionPrefix([]string{without}); got != "" {
		t.Errorf("got %q", got)
	}
}

// The compose Features Dockerfile is appended to the service's Dockerfile, so
// the global ARG must be in the prefix (before the service's first FROM), and
// the Features' content comes from the additional build context or from the
// temp content image (TS getFeaturesBuildOptions).
func TestComposeFeaturesBuild(t *testing.T) {
	fss := []*features.Set{{Features: []features.Feature{{ID: "hello", ConsecutiveId: "hello_0", Value: true}}}}
	const arg = "ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n"

	temp := imagemeta.GenerateFeaturesBuildOptions(imagemeta.FeaturesBuildParams{BaseName: "dev_container_auto_added_stage_label", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root"})
	if temp.DockerfilePrefixContent != "\n"+arg {
		t.Errorf("prefix = %q, want %q", temp.DockerfilePrefixContent, "\n"+arg)
	}
	for _, want := range []string{
		"FROM dev_container_feature_content_temp as dev_containers_feature_content_source\n",
		"FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\n",
		"COPY --chown=root:root --from=dev_containers_feature_content_source /tmp/build-features/hello_0 /tmp/dev-container-features/hello_0\n",
	} {
		if !strings.Contains(temp.DockerfileContent, want) {
			t.Errorf("content missing %q:\n%s", want, temp.DockerfileContent)
		}
	}
	if len(temp.BuildKitContexts) != 0 {
		t.Errorf("temp image build: build contexts set: %v", temp.BuildKitContexts)
	}
	if temp.BuildArgs["_DEV_CONTAINERS_BASE_IMAGE"] != "dev_container_auto_added_stage_label" {
		t.Errorf("build args = %v", temp.BuildArgs)
	}

	ctx := imagemeta.GenerateFeaturesBuildOptions(imagemeta.FeaturesBuildParams{BaseName: "base", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", UseBuildKitContexts: true, Syntax: "docker/dockerfile:1.4"})
	if ctx.DockerfilePrefixContent != "# syntax=docker/dockerfile:1.4\n"+arg {
		t.Errorf("prefix = %q", ctx.DockerfilePrefixContent)
	}
	if strings.Contains(ctx.DockerfileContent, "AS dev_containers_feature_content_source") ||
		!strings.Contains(ctx.DockerfileContent, "RUN --mount=type=bind,from=dev_containers_feature_content_source,source=hello_0,target=/tmp/build-features-src/hello_0 \\\n") {
		t.Errorf("content:\n%s", ctx.DockerfileContent)
	}
	if ctx.BuildKitContexts["dev_containers_feature_content_source"] != "." {
		t.Errorf("build contexts = %v", ctx.BuildKitContexts)
	}

	// No Features: only the metadata label, byte for byte TS getImageBuildOptions
	// (its prefix indents the ARG; the label is getDevcontainerMetadataLabel's).
	label := imagemeta.GenerateComposeLabelBuild("base", []imagemeta.Entry{{RemoteUser: "dev"}}, "")
	const tsArg = "    ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n"
	if label.DockerfilePrefixContent != "\n"+tsArg {
		t.Errorf("label-only prefix = %q", label.DockerfilePrefixContent)
	}
	if syntaxed := imagemeta.GenerateComposeLabelBuild("base", nil, "docker/dockerfile:1.4"); syntaxed.DockerfilePrefixContent != "# syntax=docker/dockerfile:1.4\n"+tsArg {
		t.Errorf("label-only prefix with syntax = %q", syntaxed.DockerfilePrefixContent)
	}
	if want := "\nFROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\nLABEL devcontainer.metadata=\"[ \\\n{\\\"remoteUser\\\":\\\"dev\\\"} \\\n]\"\n"; label.DockerfileContent != want {
		t.Errorf("label-only content = %q, want %q", label.DockerfileContent, want)
	}
}
