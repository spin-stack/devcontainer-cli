package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The values masked in logs are exactly the secrets file's values; no file, an
// unreadable one or a malformed one masks nothing.
func TestSecretValuesFromFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(good, []byte(`{"TOKEN":"s3cr=t","USER":"bob"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := secretValuesFromFile(good)
	slices.Sort(got)
	if want := []string{"bob", "s3cr=t"}; !slices.Equal(got, want) {
		t.Errorf("values = %q, want %q", got, want)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[1,2]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", filepath.Join(dir, "missing.json"), bad} {
		if got := secretValuesFromFile(p); got != nil {
			t.Errorf("secretValuesFromFile(%q) = %q, want nil", p, got)
		}
	}
}
