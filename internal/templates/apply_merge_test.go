package templates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/pfs"
)

type errFS struct {
	readErr error
}

func (e errFS) ReadFile(string) ([]byte, error)    { return nil, e.readErr }
func (errFS) WriteFile(string, []byte) error       { return nil }
func (errFS) MkdirAll(string) error                { return nil }
func (errFS) Stat(string) (os.FileInfo, error)     { return nil, nil }
func (errFS) Walk(string, filepath.WalkFunc) error { return nil }
func (errFS) Remove(string, bool) error            { return nil }

func TestMergeFeatures_PropagatesReadError(t *testing.T) {
	sentinel := errors.New("disk gone")
	feats := []TemplateFeatureOption{{ID: "ghcr.io/devcontainers/features/git:1"}}

	err := mergeFeatures(errFS{readErr: sentinel}, "/workspace", feats, log.Null)
	if err == nil {
		t.Fatal("expected error from failing ReadFile, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestMergeFeatures_PreservesJSONC(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	cfgPath := filepath.Join(dcDir, "devcontainer.json")
	original := `{
	// base image for the dev container
	"image": "mcr.microsoft.com/devcontainers/base:ubuntu",
	"forwardPorts": [3000], // app port
}
`
	os.WriteFile(cfgPath, []byte(original), 0644)

	feats := []TemplateFeatureOption{
		{ID: "ghcr.io/devcontainers/features/node:1", Options: map[string]any{"version": "20"}},
		{ID: "ghcr.io/devcontainers/features/git:1"},
	}
	if err := mergeFeatures(pfs.OSFS{}, dir, feats, log.Null); err != nil {
		t.Fatal(err)
	}

	out, _ := os.ReadFile(cfgPath)
	s := string(out)

	if !strings.Contains(s, "// base image for the dev container") {
		t.Errorf("lost leading comment:\n%s", s)
	}
	if !strings.Contains(s, "// app port") {
		t.Errorf("lost trailing comment:\n%s", s)
	}
	if !strings.Contains(s, `"ghcr.io/devcontainers/features/node:1"`) {
		t.Errorf("node feature not added:\n%s", s)
	}
	if !strings.Contains(s, `"version":"20"`) {
		t.Errorf("node options not added:\n%s", s)
	}
	if !strings.Contains(s, `"ghcr.io/devcontainers/features/git:1":"latest"`) {
		t.Errorf("git feature not added as latest:\n%s", s)
	}
	if !strings.Contains(s, `"forwardPorts": [3000]`) {
		t.Errorf("lost forwardPorts:\n%s", s)
	}
}

func TestMergeFeatures_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0755)
	cfgPath := filepath.Join(dcDir, "devcontainer.json")
	os.WriteFile(cfgPath, []byte(`{
	"image": "x",
	"features": {
		"ghcr.io/devcontainers/features/node:1": { "version": "18" }
	}
}
`), 0644)

	feats := []TemplateFeatureOption{
		{ID: "ghcr.io/devcontainers/features/node:1", Options: map[string]any{"version": "20"}},
	}
	if err := mergeFeatures(pfs.OSFS{}, dir, feats, log.Null); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(out), `"version": "18"`) || strings.Contains(string(out), `"version":"20"`) {
		t.Errorf("existing feature must NOT be overwritten:\n%s", string(out))
	}
}
