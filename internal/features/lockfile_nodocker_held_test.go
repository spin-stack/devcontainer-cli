package features

import (
	"encoding/json"
	"testing"
)

// The lockfile generated for a Feature records its dependsOn ids, and leaves
// the key out for a Feature without any, as TS generateLockfile (lockfile.ts)
// does with `dependsOn.length ? dependsOn : undefined`; the JSON is TS
// JSON.stringify(lockfile, null, 2).
func TestGenerateLockfileRecordsDependsOn(t *testing.T) {
	oci := func(id string, dependsOn map[string]any) *Set {
		return &Set{
			SourceInfo:     &OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: id, UserID: "ghcr.io/o/f/" + id + ":1"},
			Features:       []Feature{{ID: id, Version: "1.0.0", DependsOn: dependsOn}},
			ComputedDigest: "sha256:" + id,
		}
	}
	config := &Config{FeatureSets: []*Set{
		oci("a", map[string]any{"ghcr.io/o/f/b:1": map[string]any{}}),
		oci("b", nil),
	}}
	data, err := json.MarshalIndent(GenerateLockfile(config, nil), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "features": {
    "ghcr.io/o/f/a:1": {
      "version": "1.0.0",
      "resolved": "ghcr.io/o/f/a@sha256:a",
      "integrity": "sha256:a",
      "dependsOn": [
        "ghcr.io/o/f/b:1"
      ]
    },
    "ghcr.io/o/f/b:1": {
      "version": "1.0.0",
      "resolved": "ghcr.io/o/f/b@sha256:b",
      "integrity": "sha256:b"
    }
  }
}`
	if string(data) != want {
		t.Fatalf("lockfile:\n%s\nwant:\n%s", data, want)
	}
}
