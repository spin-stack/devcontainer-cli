package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildSecretsFromFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(good, []byte(`{"NPM_TOKEN":"abc","GH_TOKEN":"x=y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildSecretsFromFile(good)
	slices.Sort(got)
	if want := []string{"GH_TOKEN=x=y", "NPM_TOKEN=abc"}; !slices.Equal(got, want) {
		t.Errorf("secrets = %q, want %q", got, want)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", filepath.Join(dir, "missing.json"), bad} {
		if got := buildSecretsFromFile(p); got != nil {
			t.Errorf("buildSecretsFromFile(%q) = %q, want nil", p, got)
		}
	}
}

func TestBuildSecretIDsOneLetterName(t *testing.T) {
	if got, want := buildSecretIDs([]string{"A=1", "=2"}), []string{"A"}; !slices.Equal(got, want) {
		t.Errorf("ids = %q, want %q", got, want)
	}
}
