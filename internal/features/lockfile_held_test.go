package features

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// heldLockfile has a dependsOn array and a tarball URI with "&" in it.
func heldLockfile() *Lockfile {
	const tgz = "https://x/b.tgz?v=1&w=2"
	return &Lockfile{Features: map[string]LockfileEntry{
		"ghcr.io/o/f/a:1": {Version: "1.0.0", Resolved: "ghcr.io/o/f/a@sha256:a", Integrity: "sha256:a", DependsOn: []string{"ghcr.io/o/f/b:1", tgz}},
		tgz:               {Version: "2.0.0", Resolved: tgz, Integrity: "sha256:b"},
	}}
}

// heldLockfileJSON is what TS writeLockfile (reference/src/spec-configuration/
// lockfile.ts) writes for heldLockfile: JSON.stringify(lockfile, null, 2) +
// '\n', printed by node; "&" is not escaped.
const heldLockfileJSON = `{
  "features": {
    "ghcr.io/o/f/a:1": {
      "version": "1.0.0",
      "resolved": "ghcr.io/o/f/a@sha256:a",
      "integrity": "sha256:a",
      "dependsOn": [
        "ghcr.io/o/f/b:1",
        "https://x/b.tgz?v=1&w=2"
      ]
    },
    "https://x/b.tgz?v=1&w=2": {
      "version": "2.0.0",
      "resolved": "https://x/b.tgz?v=1&w=2",
      "integrity": "sha256:b"
    }
  }
}
`

// heldLockfileCosmetic is heldLockfileJSON as another tool could have written
// it: compact, \u escapes, no trailing newline. TS JSON.parse -> JSON.stringify
// makes it heldLockfileJSON again.
const heldLockfileCosmetic = `{"features":{"ghcr.io/o/f/a:1":{"version":"1.0.0","resolved":"ghcr.io/o/f/a@sha256:a",` +
	`"integrity":"sha256:a","dependsOn":["ghcr.io/o/f/b:1","https://x/b.tgz?v=1&w=2"]},` +
	`"https://x/b.tgz?v=1&w=2":{"version":"2.0.0","resolved":"https://x/b.tgz?v=1&w=2","integrity":"sha256:b"}}}`

// writeHeldLockfile puts content in the lockfile of a config in a new
// directory, dated an hour ago, and returns the config and lockfile paths.
func writeHeldLockfile(t *testing.T, content string) (string, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "devcontainer.json")
	lockPath := LockfilePath(configPath)
	if err := os.WriteFile(lockPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	return configPath, lockPath
}

func readHeld(t *testing.T, path string) (string, time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), info.ModTime()
}

// A new lockfile is written as TS writeLockfile writes it, which creates the
// file when there is none: the lockfile is on by default.
func TestWriteLockfileWritesWhatTSWrites(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := WriteLockfile(configPath, heldLockfile(), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := readHeld(t, LockfilePath(configPath)); got != heldLockfileJSON {
		t.Errorf("lockfile =\n%s\nwant\n%s", got, heldLockfileJSON)
	}
}

// TS writeLockfile rewrites an existing lockfile only when its JSON.parse ->
// JSON.stringify differs from the new one, and frozen fails on exactly those:
// the file TS wrote and a cosmetic variant of it are left alone and pass, a
// stale or unparsable one is replaced and fails.
func TestWriteLockfileComparesTheOldLockfileAsTSNormalizesIt(t *testing.T) {
	for _, tt := range []struct {
		name, old string
		same      bool
	}{
		{"as TS writes it", heldLockfileJSON, true},
		{"cosmetic variant", heldLockfileCosmetic, true},
		{"stale", strings.Replace(heldLockfileJSON, "2.0.0", "1.9.0", 1), false},
		{"a key order TS keeps", strings.Replace(heldLockfileCosmetic, `"version":"2.0.0","resolved":"https://x/b.tgz?v=1&w=2"`, `"resolved":"https://x/b.tgz?v=1&w=2","version":"2.0.0"`, 1), false},
		{"trailing garbage", heldLockfileJSON + "x", false},
		{"truncated", heldLockfileJSON[:40], false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configPath, lockPath := writeHeldLockfile(t, tt.old)
			_, before := readHeld(t, lockPath)
			err := WriteLockfile(configPath, heldLockfile(), WriteOptions{Frozen: true})
			if tt.same != (err == nil) {
				t.Errorf("frozen err = %v, want an error: %v", err, !tt.same)
			}
			if err := WriteLockfile(configPath, heldLockfile(), WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			got, after := readHeld(t, lockPath)
			if tt.same {
				if got != tt.old || !after.Equal(before) {
					t.Errorf("an unchanged lockfile was rewritten: %q", got)
				}
			} else if got != heldLockfileJSON {
				t.Errorf("lockfile =\n%s\nwant\n%s", got, heldLockfileJSON)
			}
		})
	}
}

// An empty features object, and the empty dependsOn TS leaves out, compare
// equal to their compact spelling: JSON.stringify writes {} and [] on one line.
func TestWriteLockfileFrozenAcceptsCompactEmptyObjects(t *testing.T) {
	for _, old := range []string{`{"features":{}}`, "{\n  \"features\": {}\n}\n"} {
		configPath, _ := writeHeldLockfile(t, old)
		if err := WriteLockfile(configPath, &Lockfile{Features: map[string]LockfileEntry{}}, WriteOptions{Frozen: true}); err != nil {
			t.Errorf("frozen against %q: %v", old, err)
		}
	}
	configPath, _ := writeHeldLockfile(t, `{"features":{},"x":[]}`)
	if err := WriteLockfile(configPath, &Lockfile{Features: map[string]LockfileEntry{}}, WriteOptions{Frozen: true}); err == nil {
		t.Error("frozen accepted a lockfile with an extra key")
	}
}
