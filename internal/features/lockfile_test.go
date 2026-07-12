package features

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateLockfile(t *testing.T) {
	config := &Config{
		FeatureSets: []*Set{
			{
				SourceInfo: &OCISource{
					Registry:  "ghcr.io",
					Namespace: "devcontainers/features",
					ID:        "go",
					UserID:    "ghcr.io/devcontainers/features/go:1",
				},
				Features:       []Feature{{ID: "go", Version: "1.21.0"}},
				ComputedDigest: "sha256:abc123",
			},
			{
				SourceInfo: &OCISource{
					Registry:  "ghcr.io",
					Namespace: "devcontainers/features",
					ID:        "node",
					UserID:    "ghcr.io/devcontainers/features/node:1",
				},
				Features:       []Feature{{ID: "node", Version: "18.0.0"}},
				ComputedDigest: "sha256:def456",
			},
			{
				SourceInfo: &LocalSource{UserID: "./local-feature"},
				Features:   []Feature{{ID: "local"}},
			},
		},
	}

	lf := GenerateLockfile(config, nil)

	if len(lf.Features) != 2 {
		t.Fatalf("features count = %d, want 2 (local should be excluded)", len(lf.Features))
	}

	goEntry, ok := lf.Features["ghcr.io/devcontainers/features/go:1"]
	if !ok {
		t.Fatal("missing go entry")
	}
	if goEntry.Version != "1.21.0" {
		t.Errorf("go version = %q", goEntry.Version)
	}
	if goEntry.Integrity != "sha256:abc123" {
		t.Errorf("go integrity = %q", goEntry.Integrity)
	}

	nodeEntry, ok := lf.Features["ghcr.io/devcontainers/features/node:1"]
	if !ok {
		t.Fatal("missing node entry")
	}
	if nodeEntry.Version != "18.0.0" {
		t.Errorf("node version = %q", nodeEntry.Version)
	}
}

// Features supplied only via --additional-features must not be
// written to the lockfile.
func TestGenerateLockfile_ExcludesAdditionalFeatures(t *testing.T) {
	config := &Config{
		FeatureSets: []*Set{
			{
				SourceInfo:     &OCISource{UserID: "ghcr.io/devcontainers/features/go:1", Registry: "ghcr.io", Namespace: "devcontainers/features", ID: "go"},
				Features:       []Feature{{ID: "go", Version: "1.21.0"}},
				ComputedDigest: "sha256:abc123",
			},
			{
				SourceInfo:     &OCISource{UserID: "ghcr.io/devcontainers/features/node:1", Registry: "ghcr.io", Namespace: "devcontainers/features", ID: "node"},
				Features:       []Feature{{ID: "node", Version: "18.0.0"}},
				ComputedDigest: "sha256:def456",
			},
		},
	}

	exclude := map[string]bool{"ghcr.io/devcontainers/features/node:1": true}
	lf := GenerateLockfile(config, exclude)

	if _, ok := lf.Features["ghcr.io/devcontainers/features/node:1"]; ok {
		t.Error("additional-feature node should be excluded from lockfile")
	}
	if _, ok := lf.Features["ghcr.io/devcontainers/features/go:1"]; !ok {
		t.Error("config feature go should remain in lockfile")
	}
	if len(lf.Features) != 1 {
		t.Fatalf("features count = %d, want 1", len(lf.Features))
	}
}

func TestGenerateLockfile_Sorted(t *testing.T) {
	config := &Config{
		FeatureSets: []*Set{
			{
				SourceInfo:     &OCISource{UserID: "z-feature", Registry: "r", Namespace: "ns", ID: "z"},
				Features:       []Feature{{Version: "1.0"}},
				ComputedDigest: "sha256:z",
			},
			{
				SourceInfo:     &OCISource{UserID: "a-feature", Registry: "r", Namespace: "ns", ID: "a"},
				Features:       []Feature{{Version: "2.0"}},
				ComputedDigest: "sha256:a",
			},
		},
	}

	lf := GenerateLockfile(config, nil)
	data, _ := json.MarshalIndent(lf, "", "  ")
	str := string(data)

	// "a-feature" should appear before "z-feature" in serialized output
	aIdx := indexOf(str, "a-feature")
	zIdx := indexOf(str, "z-feature")
	if aIdx >= zIdx {
		t.Error("entries should be sorted by ID")
	}
}

func TestReadWriteLockfile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".devcontainer", "devcontainer.json")
	os.MkdirAll(filepath.Dir(configPath), 0755)

	lf := &Lockfile{
		Features: map[string]LockfileEntry{
			"ghcr.io/devcontainers/features/go:1": {
				Version:   "1.21.0",
				Resolved:  "ghcr.io/devcontainers/features/go@sha256:abc",
				Integrity: "sha256:abc",
			},
		},
	}

	err := WriteLockfile(configPath, lf, false, true)
	if err != nil {
		t.Fatal(err)
	}

	read, init, err := ReadLockfile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if init {
		t.Error("should not be init marker")
	}
	if read == nil {
		t.Fatal("lockfile should not be nil")
	}
	if read.Features["ghcr.io/devcontainers/features/go:1"].Version != "1.21.0" {
		t.Errorf("version = %q", read.Features["ghcr.io/devcontainers/features/go:1"].Version)
	}
}

func TestReadLockfile_Empty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "devcontainer.json")
	lockPath := LockfilePath(configPath)
	os.WriteFile(lockPath, []byte("  \n"), 0644)

	_, init, err := ReadLockfile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !init {
		t.Error("empty lockfile should be init marker")
	}
}

func TestReadLockfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "devcontainer.json")

	lf, init, err := ReadLockfile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if lf != nil || init {
		t.Error("should return nil, false for missing lockfile")
	}
}

func TestGetLockfilePath(t *testing.T) {
	tests := []struct {
		config string
		want   string
	}{
		{"/project/.devcontainer/devcontainer.json", "/project/.devcontainer/devcontainer-lock.json"},
		{"/project/.devcontainer.json", "/project/.devcontainer-lock.json"},
	}
	for _, tt := range tests {
		got := LockfilePath(tt.config)
		if got != tt.want {
			t.Errorf("LockfilePath(%q) = %q, want %q", tt.config, got, tt.want)
		}
	}
}

func TestWriteLockfile_Frozen_Changed(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "devcontainer.json")
	lockPath := LockfilePath(configPath)

	// Write initial lockfile
	os.WriteFile(lockPath, []byte(`{"features":{}}`), 0644)

	lf := &Lockfile{
		Features: map[string]LockfileEntry{
			"new": {Version: "1.0"},
		},
	}

	err := WriteLockfile(configPath, lf, true, true)
	if err == nil {
		t.Error("frozen lockfile with changes should error")
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
