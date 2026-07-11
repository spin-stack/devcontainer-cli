package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

func imageResult(image, cfgPath string) *config.LoadResult {
	return &config.LoadResult{Config: &config.DevContainerConfig{Image: image, ConfigFilePath: cfgPath}}
}

func TestComputeCacheKeyFormatAndDeterminism(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	r := imageResult("ubuntu:22.04", cfgPath)

	k1, err := computeCacheKey(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(k1) {
		t.Fatalf("bad key format: %q", k1)
	}
	// Recomputing the same inputs yields the same key.
	k2, _ := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil)
	if k1 != k2 {
		t.Fatalf("non-deterministic: %q != %q", k1, k2)
	}
}

func TestComputeCacheKeyChangesWithInputs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	base, _ := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil)

	// Different image → different key.
	if k, _ := computeCacheKey(imageResult("ubuntu:24.04", cfgPath), nil); k == base {
		t.Error("image change did not affect key")
	}
	// Proxy env → different key.
	if k, _ := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), map[string]string{"HTTP_PROXY": "http://p:3128"}); k == base {
		t.Error("proxy env did not affect key")
	}
	// A non-proxy env var must NOT affect the key.
	if k, _ := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), map[string]string{"UNRELATED": "x"}); k != base {
		t.Error("unrelated env changed the key")
	}
}

func TestComputeCacheKeyIncludesDockerfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	dfPath := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dfPath, []byte("FROM ubuntu:22.04\nRUN echo v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &config.LoadResult{Config: &config.DevContainerConfig{
		ConfigFilePath: cfgPath,
		Build:          &config.BuildConfig{Dockerfile: "Dockerfile"},
	}}
	k1, err := computeCacheKey(r, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Mutating the Dockerfile flips the key.
	if err := os.WriteFile(dfPath, []byte("FROM ubuntu:22.04\nRUN echo v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k2, _ := computeCacheKey(r, nil)
	if k1 == k2 {
		t.Fatal("Dockerfile content change did not affect key")
	}
}
