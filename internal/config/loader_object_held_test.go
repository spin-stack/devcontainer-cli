package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A devcontainer.json that is not a JSON object fails as TS
// readDevContainerConfigFile fails it, naming the file; an object loads.
func TestLoadRejectsAConfigThatIsNotAnObject(t *testing.T) {
	for _, content := range []string{"null", "[1]", `"image"`, "3"} {
		ws := t.TempDir()
		cfg := filepath.Join(ws, ".devcontainer.json")
		if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadDevContainerConfig(ws, cfg, "")
		var notObject *NotObjectError
		if !errors.As(err, &notObject) || notObject.Path != cfg {
			t.Fatalf("%s: err = %v, want a NotObjectError for %s", content, err, cfg)
		}
		if want := "Dev container config (" + cfg + ") must contain a JSON object literal."; err.Error() != want {
			t.Errorf("%s: message = %q, want %q", content, err.Error(), want)
		}
	}

	ws := t.TempDir()
	base := filepath.Join(ws, "base.json")
	override := filepath.Join(ws, "override.json")
	if err := os.WriteFile(base, []byte(`{"image": "img:1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	var notObject *NotObjectError
	if _, err := LoadDevContainerConfig(ws, base, override); !errors.As(err, &notObject) || notObject.Path != base {
		t.Errorf("a null override: err = %v, want a NotObjectError naming the --config file", err)
	}
}

// TS readDevContainerConfigFile validates no property type: a falsy legacy
// "extensions", a "devPort" that customizations.vscode already sets (both left
// in place by updateFromOldProperties) and a "customizations" that is no object
// load, and the raw configuration keeps them as written.
func TestLoadAcceptsWrongTypedLegacyAndCustomizationsProperties(t *testing.T) {
	for _, tc := range []struct {
		content, key       string
		want               any
		wantCustomizations bool
	}{
		{`{"image": "a", "extensions": ""}`, "extensions", "", false},
		{`{"image": "a", "extensions": 0}`, "extensions", 0.0, false},
		{`{"image": "a", "extensions": false}`, "extensions", false, false},
		{`{"image": "a", "devPort": "x", "customizations": {"vscode": {"devPort": 1}}}`, "devPort", "x", true},
		{`{"image": "a", "customizations": ""}`, "customizations", "", false},
		{`{"image": "a", "customizations": "x"}`, "customizations", "x", false},
		{`{"image": "a", "customizations": [1]}`, "customizations", []any{1.0}, false},
	} {
		ws := t.TempDir()
		cfg := filepath.Join(ws, ".devcontainer.json")
		if err := os.WriteFile(cfg, []byte(tc.content), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := LoadDevContainerConfig(ws, cfg, "")
		if err != nil {
			t.Errorf("%s: %v", tc.content, err)
			continue
		}
		if got := res.Raw[tc.key]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: raw %s = %#v, want %#v", tc.content, tc.key, got, tc.want)
		}
		if got := res.Config.Customizations != nil; got != tc.wantCustomizations {
			t.Errorf("%s: typed customizations = %v, want present %v", tc.content, res.Config.Customizations, tc.wantCustomizations)
		}
	}
}
