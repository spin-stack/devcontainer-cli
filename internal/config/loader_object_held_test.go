package config

import (
	"errors"
	"os"
	"path/filepath"
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
