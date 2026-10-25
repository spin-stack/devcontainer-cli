package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/log"
)

// TS reports the first disallowed feature of Object.keys({...config.features,
// ...additionalFeatures}): the config file's order, then the additional
// features'. Go iterated a map, so with two disallowed features the reported id
// was random.
func TestFeatureIDsInOrderFollowsTheDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	os.WriteFile(path, []byte(`{"image":"x","features":{"z/b:1":{},"a/c:1":{}, /* jsonc */ "m/d:1":{}}}`), 0o644)
	cfg := &config.DevContainer{ConfigFilePath: path, Features: map[string]any{
		"z/b:1": map[string]any{}, "a/c:1": map[string]any{}, "m/d:1": map[string]any{},
		"y/add:1": true, "b/add:1": true, // from --additional-features
		"o/override:1": true, // in neither document
	}}
	got := featureIDsInOrder(cfg, `{"y/add:1": true, "b/add:1": true, "z/b:1": false}`)
	want := []string{"z/b:1", "a/c:1", "m/d:1", "y/add:1", "b/add:1", "o/override:1"}
	if !slices.Equal(got, want) {
		t.Errorf("featureIDsInOrder = %v, want %v", got, want)
	}
}

// up reports the disallowed feature with TS's fields: the feature id, whether it
// stopped the running container (false included, as TS emits the boolean), and
// the documentation URL, and the description says the container was stopped.
func TestEnforceDisallowedFeaturesReportsTSFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if err := os.MkdirAll(cacheFolder(), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"disallowedFeatures":[{"featureIdPrefix":"ghcr.io/o/bad","documentationURL":"https://doc"}],"featureAdvisories":[]}`
	if err := os.WriteFile(filepath.Join(cacheFolder(), "control-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "devcontainer.json")
	os.WriteFile(cfgPath, []byte(`{"features":{"ghcr.io/o/good:1":{},"ghcr.io/o/bad:2":{},"ghcr.io/o/bad/x:1":{}}}`), 0o644)
	cfg := &config.DevContainer{ConfigFilePath: cfgPath, Features: map[string]any{
		"ghcr.io/o/good:1": map[string]any{}, "ghcr.io/o/bad:2": map[string]any{}, "ghcr.io/o/bad/x:1": map[string]any{},
	}}

	stopped := enforceDisallowedFeatures(t.Context(), cfg, "", log.Null, func(_ context.Context) bool { return true })
	b, _ := json.Marshal(coreerrors.ToErrorOutput(stopped))
	desc := "Cannot use the 'ghcr.io/o/bad:2' Feature since it was reported to be problematic. Please remove this Feature from your configuration and rebuild any dev container using it before continuing. The existing dev container was stopped. See https://doc to learn more."
	want, _ := json.Marshal(map[string]any{"outcome": "error", "message": desc, "description": desc,
		"disallowedFeatureId": "ghcr.io/o/bad:2", "didStopContainer": true, "learnMoreUrl": "https://doc"})
	if !jsonEqual(b, want) {
		t.Errorf("stopped:\n got %s\nwant %s", b, want)
	}

	notStopped := enforceDisallowedFeatures(t.Context(), cfg, "", log.Null, nil)
	out := coreerrors.ToErrorOutput(notStopped)
	if out.DidStopContainer == nil || *out.DidStopContainer {
		t.Errorf("didStopContainer = %v, want false present", out.DidStopContainer)
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
