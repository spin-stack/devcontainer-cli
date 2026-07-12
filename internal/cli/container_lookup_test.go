package cli

import (
	"context"
	"strings"
	"testing"
)

// fakeLister answers ListContainers from a map of "sorted,label,filter" -> ids.
type fakeLister struct{ byFilter map[string][]string }

func (f *fakeLister) ListContainers(_ context.Context, _ bool, labels []string) ([]string, error) {
	return f.byFilter[strings.Join(labels, "|")], nil
}

func TestResolveContainerID(t *testing.T) {
	const (
		ws    = "/proj"
		cfgA  = "/proj/.devcontainer/a/devcontainer.json"
		cfgB  = "/proj/.devcontainer/b/devcontainer.json"
		local = "devcontainer.local_folder=/proj"
	)
	key := func(labels ...string) string { return strings.Join(labels, "|") }

	t.Run("config_file disambiguates multiple configs", func(t *testing.T) {
		f := &fakeLister{byFilter: map[string][]string{
			key(local, "devcontainer.config_file="+cfgA): {"idA"},
			key(local, "devcontainer.config_file="+cfgB): {"idB"},
			key(local): {"idA", "idB"}, // both share local_folder
		}}
		if got := resolveContainerID(context.Background(), f, ws, cfgB, nil); got != "idB" {
			t.Errorf("cfgB -> %q, want idB", got)
		}
		if got := resolveContainerID(context.Background(), f, ws, cfgA, nil); got != "idA" {
			t.Errorf("cfgA -> %q, want idA", got)
		}
	})

	t.Run("falls back to local_folder for legacy container (no config_file match)", func(t *testing.T) {
		f := &fakeLister{byFilter: map[string][]string{
			key(local): {"legacy"}, // no config_file-filtered entry
		}}
		if got := resolveContainerID(context.Background(), f, ws, cfgA, nil); got != "legacy" {
			t.Errorf("-> %q, want legacy (fallback)", got)
		}
	})

	t.Run("explicit id-labels win", func(t *testing.T) {
		f := &fakeLister{byFilter: map[string][]string{
			key("x=y"): {"bylabel"},
			key(local): {"byfolder"},
		}}
		if got := resolveContainerID(context.Background(), f, ws, cfgA, []string{"x=y"}); got != "bylabel" {
			t.Errorf("-> %q, want bylabel", got)
		}
	})

	t.Run("no match", func(t *testing.T) {
		if got := resolveContainerID(context.Background(), &fakeLister{}, ws, cfgA, nil); got != "" {
			t.Errorf("-> %q, want empty", got)
		}
	})
}
