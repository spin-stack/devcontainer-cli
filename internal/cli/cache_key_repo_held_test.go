package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A committed features lockfile pins resolved digests, so changing it changes
// the cache key (docs/go-only-features.md, read-configuration --cache-key); an
// empty lockfile, which pins nothing, keys like no lockfile at all.
func TestComputeCacheKeyFoldsInTheLockfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	lockPath := filepath.Join(dir, "devcontainer-lock.json")
	key := func() string {
		t.Helper()
		k, err := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	none := key()

	if err := os.WriteFile(lockPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if k := key(); k != none {
		t.Error("an empty lockfile changed the key")
	}

	lock := func(digest string) string {
		return `{"features":{"ghcr.io/o/f:1":{"version":"1.0.0","resolved":"ghcr.io/o/f@` + digest + `","integrity":"` + digest + `"}}}`
	}
	if err := os.WriteFile(lockPath, []byte(lock("sha256:aaa")), 0o644); err != nil {
		t.Fatal(err)
	}
	pinnedA := key()
	if pinnedA == none {
		t.Error("a lockfile did not change the key")
	}
	if err := os.WriteFile(lockPath, []byte(lock("sha256:bbb")), 0o644); err != nil {
		t.Fatal(err)
	}
	if key() == pinnedA {
		t.Error("a lockfile pinning another digest kept the key")
	}
}

// A proxy variable that is set but empty configures no proxy, so it keys like
// an unset one.
func TestComputeCacheKeyIgnoresEmptyProxy(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "devcontainer.json")
	base, err := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), map[string]string{"HTTPS_PROXY": ""}); k != base {
		t.Error("an empty proxy variable changed the key")
	}
}
