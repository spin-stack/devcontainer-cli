package imagemeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
)

func TestGetSafeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go", "GO"},
		{"node", "NODE"},
		{"azure-cli", "AZURE_CLI"},
		{"ghcr.io/devcontainers/features/go", "GHCR_IO_DEVCONTAINERS_FEATURES_GO"},
		{"123feature", "_FEATURE"},
		{"my_feature", "MY_FEATURE"},
	}
	for _, tt := range tests {
		got := safeID(tt.input)
		if got != tt.want {
			t.Errorf("safeID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// tsFeaturesBuildCase is testdata/ts-features-build/<case>/input.json: the
// arguments the TS functions were called with. The expected files next to it
// were produced by the TS getContainerFeaturesBaseDockerFile, getFeatureLayers,
// getDevcontainerMetadataLabel, generateContainerEnvs,
// getFeatureInstallWrapperScript and getFeatureEnvVariables (internalVersion 2)
// through testdata/ts-features-build/gen.ts:
//
//	cd reference && ./node_modules/.bin/esbuild ../internal/imagemeta/testdata/ts-features-build/gen.ts \
//	  --bundle --platform=node --main-fields=module,main --outfile=/tmp/gen.js
//	node /tmp/gen.js < ../internal/imagemeta/testdata/ts-features-build/<case>/input.json
type tsFeaturesBuildCase struct {
	UseBuildKit        bool              `json:"useBuildKit"`
	ContainerUser      string            `json:"containerUser"`
	RemoteUser         string            `json:"remoteUser"`
	Metadata           []Entry           `json:"metadata"`
	ConfigContainerEnv map[string]string `json:"configContainerEnv"`
	FeatureSets        []struct {
		SourceInformation struct {
			Type                        string `json:"type"`
			UserFeatureID               string `json:"userFeatureId"`
			UserFeatureIDWithoutVersion string `json:"userFeatureIdWithoutVersion"`
		} `json:"sourceInformation"`
		Features []json.RawMessage `json:"features"`
	} `json:"featureSets"`
}

func loadTSFeaturesBuildCase(t *testing.T, name string) (tsFeaturesBuildCase, []*features.Set, map[string]FeatureFileInfo) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ts-features-build", name, "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c tsFeaturesBuildCase
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	infos := map[string]FeatureFileInfo{}
	var sets []*features.Set
	for _, s := range c.FeatureSets {
		set := &features.Set{InternalVersion: "2"}
		switch s.SourceInformation.Type {
		case "oci":
			set.SourceInfo = &features.OCISource{Type: "oci", UserID: s.SourceInformation.UserFeatureID, UserFeatureIdWithoutVersion: s.SourceInformation.UserFeatureIDWithoutVersion}
		case "file-path":
			set.SourceInfo = &features.LocalSource{LocalPath: s.SourceInformation.UserFeatureID, UserID: s.SourceInformation.UserFeatureID}
		default:
			t.Fatalf("unsupported source type %q", s.SourceInformation.Type)
		}
		for _, raw := range s.Features {
			var f features.Feature
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			info, err := ParseFeatureFileInfo(raw)
			if err != nil {
				t.Fatal(err)
			}
			infos[f.ConsecutiveId] = info
			set.Features = append(set.Features, f)
		}
		sets = append(sets, set)
	}
	return c, sets, infos
}

func readGolden(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"testdata", "ts-features-build"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestGenerateFeaturesBuildOptions_MatchesTS compares the generated features
// Dockerfile, each Feature's devcontainer-features.env and its
// devcontainer-features-install.sh with the TS output for the same inputs, with
// BuildKit build contexts and with the temporary content image.
func TestGenerateFeaturesBuildOptions_MatchesTS(t *testing.T) {
	for _, name := range []string{"buildkit", "no-buildkit"} {
		t.Run(name, func(t *testing.T) {
			c, sets, infos := loadTSFeaturesBuildCase(t, name)
			info := GenerateFeaturesBuildOptions(FeaturesBuildParams{
				BaseName:            "base",
				FeatureSets:         sets,
				Metadata:            c.Metadata,
				ContainerUser:       c.ContainerUser,
				RemoteUser:          c.RemoteUser,
				ImageUser:           "imguser",
				UseBuildKitContexts: c.UseBuildKit,
				ConfigContainerEnv:  c.ConfigContainerEnv,
			})
			if want := readGolden(t, name, "Dockerfile"); info.DockerfileContent != want {
				t.Errorf("Dockerfile differs from TS:\n--- got\n%s\n--- want\n%s", info.DockerfileContent, want)
			}
			for _, set := range sets {
				for _, f := range set.Features {
					vars := FeatureEnvVariables(f, infos[f.ConsecutiveId])
					if got, want := strings.Join(vars, "\n"), readGolden(t, name, f.ConsecutiveId, FeatureEnvFile); got != want {
						t.Errorf("%s env differs from TS:\n--- got\n%s\n--- want\n%s", f.ConsecutiveId, got, want)
					}
					got := FeatureInstallWrapperScript(f, set, infos[f.ConsecutiveId], vars)
					if want := readGolden(t, name, f.ConsecutiveId, FeatureInstallWrapperFile); got != want {
						t.Errorf("%s install wrapper differs from TS:\n--- got\n%s\n--- want\n%s", f.ConsecutiveId, got, want)
					}
				}
			}
			wantArgs := map[string]string{
				"_DEV_CONTAINERS_BASE_IMAGE":             "base",
				"_DEV_CONTAINERS_IMAGE_USER":             "imguser",
				"_DEV_CONTAINERS_FEATURE_CONTENT_SOURCE": ComposeFeatureContentImage,
			}
			if len(info.BuildArgs) != len(wantArgs) {
				t.Errorf("build args = %v, want %v", info.BuildArgs, wantArgs)
			}
			for k, v := range wantArgs {
				if info.BuildArgs[k] != v {
					t.Errorf("build arg %s = %q, want %q", k, info.BuildArgs[k], v)
				}
			}
			_, hasCtx := info.BuildKitContexts["dev_containers_feature_content_source"]
			if hasCtx != c.UseBuildKit {
				t.Errorf("build contexts = %v with BuildKit %v", info.BuildKitContexts, c.UseBuildKit)
			}
			if info.OverrideTarget != "dev_containers_target_stage" {
				t.Errorf("target = %q", info.OverrideTarget)
			}
		})
	}
}

// TS getFeaturesBuildOptions dockerfilePrefixContent: the syntax line (if any)
// followed by a newline and the global ARG.
func TestGenerateFeaturesBuildOptions_Prefix(t *testing.T) {
	fss := []*features.Set{{SourceInfo: &features.OCISource{}, Features: []features.Feature{{ID: "go", ConsecutiveId: "go_0"}}}}
	if got := GenerateFeaturesBuildOptions(FeaturesBuildParams{FeatureSets: fss, Syntax: "docker/dockerfile:1.4"}).DockerfilePrefixContent; got != "# syntax=docker/dockerfile:1.4\nARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n" {
		t.Errorf("prefix with syntax = %q", got)
	}
	if got := GenerateFeaturesBuildOptions(FeaturesBuildParams{FeatureSets: fss}).DockerfilePrefixContent; got != "\nARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n" {
		t.Errorf("prefix without syntax = %q", got)
	}
	// The image user defaults to root, as TS imageBuildInfo.user does.
	if got := GenerateFeaturesBuildOptions(FeaturesBuildParams{FeatureSets: fss}).BuildArgs["_DEV_CONTAINERS_IMAGE_USER"]; got != "root" {
		t.Errorf("image user = %q", got)
	}
}

// TestExtend_SyntaxDirectiveForBuildContexts: the syntax the caller chose is
// the first line, and a build-context build declares the feature-content
// context; without a syntax no directive is forced.
func TestExtend_SyntaxDirectiveForBuildContexts(t *testing.T) {
	fss := []*features.Set{{
		SourceInfo: &features.OCISource{ID: "go"},
		Features:   []features.Feature{{ID: "go", ConsecutiveId: "go_0", Version: "1.21", Value: true}},
	}}

	withCtx := GenerateFeaturesBuildOptions(FeaturesBuildParams{BaseName: "base:img", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", UseBuildKitContexts: true, Syntax: "docker/dockerfile:1.4"})
	full := withCtx.DockerfilePrefixContent + withCtx.DockerfileContent
	if !strings.HasPrefix(full, "# syntax=docker/dockerfile:1.4\n") {
		t.Errorf("build-context build must start with the syntax directive, got:\n%s", full)
	}
	if _, ok := withCtx.BuildKitContexts["dev_containers_feature_content_source"]; !ok {
		t.Error("expected the feature-content build context to be declared")
	}

	noCtx := GenerateFeaturesBuildOptions(FeaturesBuildParams{BaseName: "base:img", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root"})
	if strings.Contains(noCtx.DockerfilePrefixContent, "syntax=") {
		t.Errorf("non-context build must not force a syntax directive: %q", noCtx.DockerfilePrefixContent)
	}
}

// TestExtend_BuildSecretsMountedIntoInstall guards #1078 (no TS equivalent):
// build-secret ids are mounted into each feature-install RUN as
// --mount=type=secret,id=<id>, and absent when no secrets are provided.
func TestExtend_BuildSecretsMountedIntoInstall(t *testing.T) {
	fss := []*features.Set{{
		SourceInfo: &features.OCISource{ID: "go"},
		Features:   []features.Feature{{ID: "go", ConsecutiveId: "go_0", Version: "1.21", Value: true}},
	}}

	withSecrets := GenerateFeaturesBuildOptions(FeaturesBuildParams{BaseName: "base:img", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", UseBuildKitContexts: true, SecretIDs: []string{"NPM_TOKEN", "GH_TOKEN"}})
	if want := "RUN --mount=type=bind,from=dev_containers_feature_content_source,source=go_0,target=/tmp/build-features-src/go_0 --mount=type=secret,id=NPM_TOKEN --mount=type=secret,id=GH_TOKEN \\\n"; !strings.Contains(withSecrets.DockerfileContent, want) {
		t.Errorf("BuildKit feature install RUN missing secret mounts:\n%s", withSecrets.DockerfileContent)
	}
	noCtx := GenerateFeaturesBuildOptions(FeaturesBuildParams{BaseName: "base:img", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", SecretIDs: []string{"NPM_TOKEN"}})
	if want := "RUN --mount=type=secret,id=NPM_TOKEN chmod -R 0755 /tmp/dev-container-features/go_0 \\\n"; !strings.Contains(noCtx.DockerfileContent, want) {
		t.Errorf("content-image feature install RUN missing secret mounts:\n%s", noCtx.DockerfileContent)
	}

	noSecrets := GenerateFeaturesBuildOptions(FeaturesBuildParams{BaseName: "base:img", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", UseBuildKitContexts: true})
	if strings.Contains(noSecrets.DockerfileContent, "type=secret") {
		t.Errorf("no secrets provided, but a secret mount leaked in:\n%s", noSecrets.DockerfileContent)
	}
}

// TS getFeatureValueObject: option defaults in document order, overlaid with
// the user's object; a string or boolean value sets "version" only when the
// Feature declares that option.
func TestFeatureEnvVariables(t *testing.T) {
	opts := map[string]any{
		"version": map[string]any{"type": "string", "default": "lts"},
		"install": map[string]any{"type": "boolean", "default": true},
		"noDef":   map[string]any{"type": "string"},
		"list":    map[string]any{"type": "string", "default": nil},
	}
	info := FeatureFileInfo{OptionOrder: []string{"version", "install", "noDef", "list"}}
	tests := []struct {
		name  string
		value any
		opts  map[string]any
		want  []string
	}{
		{"object", map[string]any{"install": false, "extra-opt": 1.5}, opts, []string{`VERSION="lts"`, `INSTALL="false"`, `LIST="null"`, `EXTRA_OPT="1.5"`}},
		{"string", "18", opts, []string{`VERSION="18"`, `INSTALL="true"`, `LIST="null"`}},
		{"true with version", true, opts, []string{`VERSION="true"`, `INSTALL="true"`, `LIST="null"`}},
		{"string without version", "18", map[string]any{"a": map[string]any{"default": "x"}}, []string{`A="x"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := info
			if _, ok := tt.opts["version"]; !ok {
				order = FeatureFileInfo{OptionOrder: []string{"a"}}
			}
			got := FeatureEnvVariables(features.Feature{ID: "x", Value: tt.value, Options: tt.opts}, order)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseFeatureFileInfo(t *testing.T) {
	info, err := ParseFeatureFileInfo([]byte(`{"id":"x","deprecated":true,"currentId":"y","options":{"zeta":{},"alpha":{"default":1},"mid":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(info.OptionOrder, ",") != "zeta,alpha,mid" || !info.Deprecated || info.CurrentID != "y" {
		t.Errorf("info = %+v", info)
	}
	if _, err := ParseFeatureFileInfo([]byte(`[]`)); err == nil {
		t.Error("a non-object document must be an error")
	}
}
