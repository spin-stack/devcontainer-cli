package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// A legacy property TS updateFromOldProperties leaves in place stays in the
// configuration read-configuration prints: a devPort whose
// customizations.vscode already has one, and a falsy settings. The TS CLI
// prints both (reference read-configuration of this config); a null devPort,
// which TS moves into customizations.vscode, is never printed at the top level.
func TestConfigurationOutputKeepsUnmigratedLegacyProperties(t *testing.T) {
	_, result := loadFixtureConfig(t, `{
		"image": "img:1",
		"devPort": 3000,
		"settings": false,
		"customizations": {"vscode": {"devPort": 1}}
	}`, nil)
	got := configurationOutput(result, &readConfigOpts{})
	if got["devPort"] != float64(3000) || got["settings"] != false {
		t.Errorf("devPort = %v, settings = %v; want 3000 and false kept", got["devPort"], got["settings"])
	}
	if vscode := got["customizations"].(map[string]any)["vscode"]; !reflect.DeepEqual(vscode, map[string]any{"devPort": float64(1)}) {
		t.Errorf("customizations.vscode = %v, want its own devPort", vscode)
	}

	_, result = loadFixtureConfig(t, `{"image": "img:1", "devPort": null, "x-null": null}`, nil)
	got = configurationOutput(result, &readConfigOpts{})
	if v, ok := got["devPort"]; ok {
		t.Errorf("devPort = %v, want a null devPort left out", v)
	}
	if v, ok := got["x-null"]; !ok || v != nil {
		t.Errorf("x-null = %v (%v), want an unknown null property kept", v, ok)
	}
}

// The config's forwardPorts are part of its metadata entry, a single one too.
func TestConfigMetadataEntryForwardsASinglePort(t *testing.T) {
	var cfg config.DevContainer
	if err := json.Unmarshal([]byte(`{"forwardPorts": [3000]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(configMetadataEntry(&cfg, false))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"forwardPorts":[3000]}`; string(b) != want {
		t.Errorf("entry = %s, want %s", b, want)
	}
}
