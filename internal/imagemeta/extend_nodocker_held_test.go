package imagemeta

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
)

// TestParseFeatureFileInfoIgnoresNonObjectOptions: only an options object has
// keys to order (TS Object.keys of feature.options in getFeatureValueDefaults,
// reference/src/spec-configuration/containerFeaturesConfiguration.ts); absent,
// null or array options yield no order.
func TestParseFeatureFileInfoIgnoresNonObjectOptions(t *testing.T) {
	for _, doc := range []string{`{"id":"x"}`, `{"id":"x","options":null}`, `{"id":"x","options":["a","b"]}`} {
		info, err := ParseFeatureFileInfo([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		if len(info.OptionOrder) != 0 {
			t.Errorf("%s: option order = %q, want none", doc, info.OptionOrder)
		}
	}
}

// TestFeatureEnvVariablesJoinArraysAsJS: an array option value is written as a
// JS template literal writes it (TS getFeatureEnvVariables,
// containerFeaturesConfiguration.ts): elements joined by commas, null as empty.
func TestFeatureEnvVariablesJoinArraysAsJS(t *testing.T) {
	feat := features.Feature{
		ID:      "x",
		Value:   map[string]any{"list": []any{float64(1), "a", nil, true}},
		Options: map[string]any{"list": map[string]any{"type": "string"}},
	}
	got := FeatureEnvVariables(feat, FeatureFileInfo{OptionOrder: []string{"list"}})
	if want := []string{`LIST="1,a,,true"`}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFeatureInstallWrapperScriptRenameWarning: TS getFeatureInstallWrapperScript
// (containerFeaturesConfiguration.ts) warns about a rename only when the Feature
// has legacyIds, a currentId, and an id other than that currentId.
func TestFeatureInstallWrapperScriptRenameWarning(t *testing.T) {
	const warning = `(!) WARNING: This feature has been renamed. Please update the reference in devcontainer.json to "new".`
	tests := []struct {
		name      string
		legacyIDs []string
		id        string
		currentID string
		want      bool
	}{
		{"renamed", []string{"older"}, "old", "new", true},
		{"no legacy ids", nil, "old", "new", false},
		{"no current id", []string{"older"}, "old", "", false},
		{"already the current id", []string{"older"}, "new", "new", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feat := features.Feature{ID: tt.id, LegacyIds: tt.legacyIDs}
			script := FeatureInstallWrapperScript(feat, &features.Set{}, FeatureFileInfo{CurrentID: tt.currentID}, nil)
			if got := strings.Contains(script, "has been renamed"); got != tt.want {
				t.Errorf("rename warning = %v, want %v:\n%s", got, tt.want, script)
			}
			if tt.want && !strings.Contains(script, "echo '"+warning+"'") {
				t.Errorf("script lacks %q:\n%s", warning, script)
			}
		})
	}
}

// TestGenerateFeaturesBuildOptionsWritesASingleContainerEnv: TS
// generateContainerEnvs (containerFeaturesConfiguration.ts) writes one ENV line
// per variable - a lone variable too - escaping quotes and backslashes, and
// dollar signs only for the config's containerEnv (#{containerEnvMetadata},
// containerFeatures.ts getFeaturesBuildOptions).
func TestGenerateFeaturesBuildOptionsWritesASingleContainerEnv(t *testing.T) {
	fss := []*features.Set{{
		SourceInfo: &features.OCISource{ID: "go"},
		Features:   []features.Feature{{ID: "go", ConsecutiveId: "go_0", Value: true, ContainerEnv: map[string]string{"PATH": "/x:${PATH}"}}},
	}}
	got := GenerateFeaturesBuildOptions(FeaturesBuildParams{
		BaseName: "base", FeatureSets: fss, ContainerUser: "root", RemoteUser: "root", UseBuildKitContexts: true,
		ConfigContainerEnv: map[string]string{"GREETING": `a"b\c$d`},
	}).DockerfileContent
	if want := "ENV PATH=\"/x:${PATH}\"\nRUN --mount=type=bind"; !strings.Contains(got, want) {
		t.Errorf("Dockerfile lacks the Feature's ENV line %q:\n%s", want, got)
	}
	if want := "\n\nENV GREETING=\"a\\\"b\\\\c\\$d\"\n"; !strings.HasSuffix(got, want) {
		t.Errorf("Dockerfile does not end with the config's ENV line %q:\n%s", want, got)
	}
}
