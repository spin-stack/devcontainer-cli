package cli

import (
	"context"
	"errors"
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
		if got, err := resolveContainerID(context.Background(), f, ws, cfgB, nil); err != nil || got != "idB" {
			t.Errorf("cfgB -> %q, want idB", got)
		}
		if got, err := resolveContainerID(context.Background(), f, ws, cfgA, nil); err != nil || got != "idA" {
			t.Errorf("cfgA -> %q, want idA", got)
		}
	})

	t.Run("falls back to local_folder for legacy container (no config_file match)", func(t *testing.T) {
		f := &fakeLister{byFilter: map[string][]string{
			key(local): {"legacy"}, // no config_file-filtered entry
		}}
		if got, err := resolveContainerID(context.Background(), f, ws, cfgA, nil); err != nil || got != "legacy" {
			t.Errorf("-> %q, want legacy (fallback)", got)
		}
	})

	t.Run("explicit id-labels win", func(t *testing.T) {
		f := &fakeLister{byFilter: map[string][]string{
			key("x=y"): {"bylabel"},
			key(local): {"byfolder"},
		}}
		if got, err := resolveContainerID(context.Background(), f, ws, cfgA, []string{"x=y"}); err != nil || got != "bylabel" {
			t.Errorf("-> %q, want bylabel", got)
		}
	})

	t.Run("no match", func(t *testing.T) {
		if got, err := resolveContainerID(context.Background(), &fakeLister{}, ws, cfgA, nil); err != nil || got != "" {
			t.Errorf("-> %q, want empty", got)
		}
	})

	t.Run("a failed lookup is TS's failed docker ps", func(t *testing.T) {
		boom := errors.New("boom")
		failing := failingLister{boom}
		for name, tc := range map[string]struct {
			ws, cfg string
			labels  []string
			want    string
		}{
			"id labels":             {ws, cfgA, []string{"x=y", "a=b"}, "Command failed: docker ps -q -a --filter label=x=y --filter label=a=b"},
			"folder and config":     {ws, cfgA, nil, "Command failed: docker ps -q -a --filter label=" + local + " --filter label=devcontainer.config_file=" + cfgA},
			"folder without config": {ws, "", nil, "Command failed: docker ps -q -a --filter label=" + local},
		} {
			got, err := resolveContainerID(context.Background(), failing, tc.ws, tc.cfg, tc.labels)
			if got != "" || err == nil || err.Error() != tc.want || !errors.Is(err, boom) {
				t.Errorf("%s: %q, %v; want %q wrapping the engine's error", name, got, err, tc.want)
			}
		}
		// The fallback lookup failing after a config-file miss fails too.
		half := &halfLister{fail: key(local), err: boom}
		if _, err := resolveContainerID(context.Background(), half, ws, cfgA, nil); err == nil || err.Error() != "Command failed: docker ps -q -a --filter label="+local {
			t.Errorf("fallback lookup: err = %v", err)
		}
	})
}

// halfLister finds nothing, and fails the lookup by the fail filter.
type halfLister struct {
	fail string
	err  error
}

func (h *halfLister) ListContainers(_ context.Context, _ bool, labels []string) ([]string, error) {
	if strings.Join(labels, "|") == h.fail {
		return nil, h.err
	}
	return nil, nil
}
