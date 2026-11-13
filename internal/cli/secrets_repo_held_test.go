package cli

import (
	"slices"
	"testing"
)

func TestBuildSecretsFromFile(t *testing.T) {
	s, err := loadSecretsFile(writeSecretsFile(t, `{"NPM_TOKEN":"abc","GH_TOKEN":"x=y"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := s.env()
	slices.Sort(got)
	if want := []string{"GH_TOKEN=x=y", "NPM_TOKEN=abc"}; !slices.Equal(got, want) {
		t.Errorf("secrets = %q, want %q", got, want)
	}
	for _, content := range []string{`not json`, `{"A":"1",}`} {
		if s, err := loadSecretsFile(writeSecretsFile(t, content)); err == nil {
			t.Errorf("loadSecretsFile(%q) = %q, want an error", content, s)
		}
	}
	if s, err := loadSecretsFile(""); err != nil || s.env() != nil {
		t.Errorf("no --secrets-file = %q, %v; want no secrets", s.env(), err)
	}
}

func TestBuildSecretIDsOneLetterName(t *testing.T) {
	if got, want := buildSecretIDs([]string{"A=1", "=2"}), []string{"A"}; !slices.Equal(got, want) {
		t.Errorf("ids = %q, want %q", got, want)
	}
}
