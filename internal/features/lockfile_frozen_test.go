package features

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A frozen lockfile fails with TS writeLockfile's own errors: "Lockfile does
// not exist." when there is none (without writing one) and "Lockfile does not match." when it is stale.
func TestWriteLockfileFrozenErrors(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "devcontainer.json")
	err := WriteLockfile(configPath, heldLockfile(), WriteOptions{Frozen: true})
	if !errors.Is(err, ErrLockfileMissing) || err.Error() != "Lockfile does not exist." {
		t.Errorf("frozen without a lockfile: err = %v", err)
	}
	if _, statErr := os.Stat(LockfilePath(configPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("frozen wrote a lockfile: %v", statErr)
	}

	configPath, _ = writeHeldLockfile(t, `{"features":{"stale":{}}}`)
	err = WriteLockfile(configPath, heldLockfile(), WriteOptions{Frozen: true})
	if !errors.Is(err, ErrLockfileMismatch) || err.Error() != "Lockfile does not match." {
		t.Errorf("frozen with a stale lockfile: err = %v", err)
	}
}
