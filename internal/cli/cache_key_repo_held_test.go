package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
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

// A lockfile that is not JSON fails the key instead of keying as if no
// lockfile were committed: the reference's readLockfile rethrows the parse
// error, and a key without the lockfile would not track its pinned digests.
func TestComputeCacheKeyFailsOnAMalformedLockfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	lockPath := filepath.Join(dir, "devcontainer-lock.json")
	if err := os.WriteFile(lockPath, []byte(`{"features":`), 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil)
	if err == nil {
		t.Fatalf("key = %q, want the lockfile parse error", key)
	}
	if want := "read features lockfile " + lockPath + ": Unexpected end of JSON input"; err.Error() != want {
		t.Errorf("err = %v, want %q: the lockfile and the reference's parse error", err, want)
	}
}

// A lockfile that exists but cannot be read fails the key too: only a missing
// lockfile (ENOENT) means none is committed.
func TestComputeCacheKeyFailsOnAnUnreadableLockfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devcontainer.json")
	lockPath := filepath.Join(dir, "devcontainer-lock.json")
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := computeCacheKey(imageResult("ubuntu:22.04", cfgPath), nil); err == nil || !strings.Contains(err.Error(), lockPath) {
		t.Errorf("err = %v, want the read failure naming %s", err, lockPath)
	}
}

// A Dockerfile-based configuration whose Dockerfile cannot be read fails the
// key instead of keying as if it had no Dockerfile, naming the file.
func TestComputeCacheKeyFailsOnAMissingDockerfile(t *testing.T) {
	dir := t.TempDir()
	r := &config.LoadResult{Config: &config.DevContainer{
		Build:          &config.Build{Dockerfile: "Dockerfile"},
		ConfigFilePath: filepath.Join(dir, "devcontainer.json"),
	}}
	key, err := computeCacheKey(r, nil)
	if want := "read Dockerfile: open " + filepath.Join(dir, "Dockerfile") + ": no such file or directory"; err == nil || err.Error() != want {
		t.Errorf("key, err = %q, %v; want %q", key, err, want)
	}
}
