package templates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
)

func buildTemplateTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		content := entries[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeTemplateRegistry struct {
	blob        []byte
	layers      []oci.Layer
	manifestErr error
	blobErr     error
}

func (f *fakeTemplateRegistry) FetchManifest(ref *oci.Ref, expectedDigest string) (*oci.ManifestContainer, error) {
	if f.manifestErr != nil {
		return nil, f.manifestErr
	}
	layers := f.layers
	if layers == nil {
		layers = []oci.Layer{{
			MediaType: "application/vnd.devcontainers.layer.v1+tar",
			Digest:    "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Size:      int64(len(f.blob)),
		}}
	}
	return &oci.ManifestContainer{
		Manifest:    &oci.Manifest{SchemaVersion: 2, Layers: layers},
		CanonicalID: ref.Resource + "@sha256:1111",
	}, nil
}

func (f *fakeTemplateRegistry) FetchBlob(ref *oci.Ref, digest string) ([]byte, error) {
	if f.blobErr != nil {
		return nil, f.blobErr
	}
	return f.blob, nil
}

func (f *fakeTemplateRegistry) GetPublishedTags(ref *oci.Ref) ([]string, error) {
	return nil, fmt.Errorf("GetPublishedTags not used")
}

func (f *fakeTemplateRegistry) GetPublishedTagsContext(_ context.Context, ref *oci.Ref) ([]string, error) {
	return nil, fmt.Errorf("GetPublishedTagsContext not used")
}

func (f *fakeTemplateRegistry) PushArtifact(ctx context.Context, ref *oci.Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*oci.PushResult, error) {
	return nil, fmt.Errorf("PushArtifact not used")
}

func (f *fakeTemplateRegistry) PushCollectionMetadata(ctx context.Context, ref *oci.Ref, collectionJSONPath string) (*oci.PushResult, error) {
	return nil, fmt.Errorf("PushCollectionMetadata not used")
}

type failFS struct {
	pfs.FS
	onWrite func(path string) error
	onMkdir func(path string) error
	writes  []string
}

func (f *failFS) WriteFile(path string, data []byte) error {
	if f.onWrite != nil {
		if err := f.onWrite(path); err != nil {
			return err
		}
	}
	if err := f.FS.WriteFile(path, data); err != nil {
		return err
	}
	f.writes = append(f.writes, path)
	return nil
}

func (f *failFS) MkdirAll(path string) error {
	if f.onMkdir != nil {
		if err := f.onMkdir(path); err != nil {
			return err
		}
	}
	return f.FS.MkdirAll(path)
}

func templateEntries() map[string]string {
	return map[string]string{
		"devcontainer-template.json": `{
			"id": "sample",
			"options": {
				"imageVariant": { "type": "string", "default": "bookworm" }
			}
		}`,
		".devcontainer/devcontainer.json": `{
	"image": "mcr.microsoft.com/devcontainers/base:${templateOption:imageVariant}"
}`,
		"Dockerfile":       "FROM base:${templateOption:imageVariant}\n",
		"scripts/setup.sh": "#!/bin/sh\necho ${templateOption:imageVariant}\n",
		"README.md":        "docs, must be omitted",
	}
}

func newFetchParams(t *testing.T, fsys pfs.FS, reg oci.Registry, workspace string) ApplyParams {
	t.Helper()
	return ApplyParams{
		OCIClient:       reg,
		FS:              fsys,
		Logger:          log.Null,
		WorkspaceFolder: workspace,
		TmpDir:          t.TempDir(),
	}
}

func TestFetchAndApply_Success(t *testing.T) {
	workspace := t.TempDir()
	reg := &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())}
	params := newFetchParams(t, pfs.OSFS{}, reg, workspace)

	applied, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/devcontainers/templates/sample:1"})
	if err != nil {
		t.Fatalf("FetchAndApply: %v", err)
	}

	sort.Strings(applied)
	want := []string{"./.devcontainer/devcontainer.json", "./Dockerfile", "./scripts/setup.sh"}
	if strings.Join(applied, ",") != strings.Join(want, ",") {
		t.Fatalf("applied = %v, want %v", applied, want)
	}

	df, _ := os.ReadFile(filepath.Join(workspace, "Dockerfile"))
	if !strings.Contains(string(df), "base:bookworm") {
		t.Errorf("Dockerfile not substituted with default: %q", df)
	}
	if _, err := os.Stat(filepath.Join(workspace, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md should have been omitted from the workspace")
	}
}

func TestFetchAndApply_DefaultTempDirIsRemoved(t *testing.T) {
	tests := []struct {
		name    string
		blob    func(*testing.T) []byte
		wantErr bool
	}{
		{
			name: "success",
			blob: func(t *testing.T) []byte {
				return buildTemplateTarGz(t, templateEntries())
			},
		},
		{
			name: "extraction error",
			blob: func(*testing.T) []byte {
				return []byte("not a valid gzip tarball")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			t.Setenv("TMPDIR", tempRoot)
			params := ApplyParams{
				OCIClient:       &fakeTemplateRegistry{blob: tt.blob(t)},
				FS:              pfs.OSFS{},
				Logger:          log.Null,
				WorkspaceFolder: t.TempDir(),
			}

			_, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/devcontainers/templates/sample:1"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchAndApply() error = %v, wantErr %v", err, tt.wantErr)
			}
			entries, readErr := os.ReadDir(tempRoot)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("default temp root contains %v after FetchAndApply", entries)
			}
		})
	}
}

func TestFetchAndApply_CallerTempDirIsPreserved(t *testing.T) {
	tempRoot := t.TempDir()
	params := ApplyParams{
		OCIClient:       &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())},
		FS:              pfs.OSFS{},
		Logger:          log.Null,
		WorkspaceFolder: t.TempDir(),
		TmpDir:          tempRoot,
	}

	_, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/devcontainers/templates/sample:1"})
	if err != nil {
		t.Fatalf("FetchAndApply: %v", err)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "template-sample" {
		t.Fatalf("caller temp root contains %v, want preserved template-sample directory", entries)
	}
}

func TestFetchAndApply_PartialWorkspaceWrite(t *testing.T) {
	workspace := t.TempDir()
	reg := &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())}

	wsWrites := 0
	ff := &failFS{FS: pfs.OSFS{}}
	ff.onWrite = func(path string) error {
		if strings.HasPrefix(path, workspace) {
			wsWrites++
			if wsWrites == 2 {
				return fmt.Errorf("no space left on device")
			}
		}
		return nil
	}
	params := newFetchParams(t, ff, reg, workspace)

	applied, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/devcontainers/templates/sample:1"})
	if err == nil {
		t.Fatal("expected a partial-write error, got nil")
	}
	if applied != nil {
		t.Errorf("applied files must be nil on error, got %v", applied)
	}
	if !strings.Contains(err.Error(), "apply template files") || !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("error = %v, want wrapped 'apply template files' + injected cause", err)
	}

	var onDisk []string
	filepath.Walk(workspace, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			onDisk = append(onDisk, p)
		}
		return nil
	})
	if len(onDisk) != 1 {
		t.Fatalf("workspace has %d files (%v), want exactly 1 partial file", len(onDisk), onDisk)
	}
}

func TestFetchAndApply_MergeFeaturesWriteError(t *testing.T) {
	workspace := t.TempDir()
	reg := &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())}

	configPath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	ff := &failFS{FS: pfs.OSFS{}}
	ff.onWrite = func(path string) error {
		if path == configPath && contains(ff.writes, configPath) {
			return fmt.Errorf("config became read-only")
		}
		return nil
	}
	params := newFetchParams(t, ff, reg, workspace)

	_, err := FetchAndApply(params, SelectedTemplate{
		ID:       "ghcr.io/devcontainers/templates/sample:1",
		Features: []TemplateFeatureOption{{ID: "ghcr.io/devcontainers/features/node:1"}},
	})
	if err == nil {
		t.Fatal("expected a merge-features error, got nil")
	}
	if !strings.Contains(err.Error(), "merge features into config") {
		t.Fatalf("error = %v, want wrapped 'merge features into config'", err)
	}
}

func contains(xs []string, x string) bool {
	return slices.Contains(xs, x)
}

func TestFetchAndApply_FetchSideErrors(t *testing.T) {
	goodBlob := buildTemplateTarGz(t, templateEntries())

	tests := []struct {
		name     string
		id       string
		reg      *fakeTemplateRegistry
		wantWrap string
	}{
		{
			name:     "invalid ref",
			id:       ".bad-ref-starts-with-dot",
			reg:      &fakeTemplateRegistry{blob: goodBlob},
			wantWrap: "parse template ID",
		},
		{
			name:     "manifest fetch fails",
			id:       "ghcr.io/x/y:1",
			reg:      &fakeTemplateRegistry{manifestErr: fmt.Errorf("401 unauthorized")},
			wantWrap: "fetch template manifest",
		},
		{
			name:     "manifest has no layers",
			id:       "ghcr.io/x/y:1",
			reg:      &fakeTemplateRegistry{layers: []oci.Layer{}},
			wantWrap: "template manifest has no layers",
		},
		{
			name:     "blob fetch fails",
			id:       "ghcr.io/x/y:1",
			reg:      &fakeTemplateRegistry{blobErr: fmt.Errorf("connection reset")},
			wantWrap: "fetch template blob",
		},
		{
			name:     "corrupt tarball",
			id:       "ghcr.io/x/y:1",
			reg:      &fakeTemplateRegistry{blob: []byte("not a valid gzip tarball")},
			wantWrap: "extract template tarball",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := newFetchParams(t, pfs.OSFS{}, tt.reg, t.TempDir())
			_, err := FetchAndApply(params, SelectedTemplate{ID: tt.id})
			if err == nil {
				t.Fatalf("expected error wrapping %q, got nil", tt.wantWrap)
			}
			if !strings.Contains(err.Error(), tt.wantWrap) {
				t.Fatalf("error = %v, want wrap %q", err, tt.wantWrap)
			}
		})
	}
}

func TestFetchAndApply_WriteTarballError(t *testing.T) {
	reg := &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())}
	ff := &failFS{FS: pfs.OSFS{}}
	ff.onWrite = func(path string) error {
		if strings.HasSuffix(path, "template.tar") {
			return fmt.Errorf("read-only tmp")
		}
		return nil
	}
	params := newFetchParams(t, ff, reg, t.TempDir())
	_, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/x/y:1"})
	if err == nil || !strings.Contains(err.Error(), "write tarball") {
		t.Fatalf("error = %v, want wrapped 'write tarball'", err)
	}
}

func TestFetchAndApply_MkdirError(t *testing.T) {
	reg := &fakeTemplateRegistry{blob: buildTemplateTarGz(t, templateEntries())}
	ff := &failFS{FS: pfs.OSFS{}}
	ff.onMkdir = func(path string) error {
		if strings.Contains(path, "template-") {
			return fmt.Errorf("permission denied")
		}
		return nil
	}
	params := newFetchParams(t, ff, reg, t.TempDir())
	_, err := FetchAndApply(params, SelectedTemplate{ID: "ghcr.io/x/y:1"})
	if err == nil || !strings.Contains(err.Error(), "create extract dir") {
		t.Fatalf("error = %v, want wrapped 'create extract dir'", err)
	}
}

func TestApplyOptionDefaults_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-template.json"), []byte(`{ this is not json `), 0o644); err != nil {
		t.Fatal(err)
	}
	got := applyOptionDefaults(pfs.OSFS{}, dir, map[string]string{"a": "b"}, log.Null)
	if len(got) != 1 || got["a"] != "b" {
		t.Errorf("got %v, want the user options unchanged", got)
	}
}

func TestMergeFeatures_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{ "image": }`), 0o644); err != nil {
		t.Fatal(err)
	}
	feats := []TemplateFeatureOption{{ID: "ghcr.io/devcontainers/features/git:1"}}
	err := mergeFeatures(pfs.OSFS{}, dir, feats, log.Null)
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error = %v, want a wrapped parse error", err)
	}
}

func TestMergeFeatures_NoConfig(t *testing.T) {
	dir := t.TempDir()
	feats := []TemplateFeatureOption{{ID: "ghcr.io/devcontainers/features/git:1"}}
	if err := mergeFeatures(pfs.OSFS{}, dir, feats, log.Null); err != nil {
		t.Fatalf("mergeFeatures with no config should be a no-op, got %v", err)
	}
}
