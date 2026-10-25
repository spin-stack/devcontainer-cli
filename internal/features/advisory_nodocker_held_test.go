package features

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// A failed fetch with nothing cached caches the empty manifest TS
// updateControlManifest writes (JSON.stringify(emptyControlManifest,
// undefined, 2)), so a second call within five minutes reads it instead of
// fetching again.
func TestGetControlManifestCachesTheEmptyManifestOnFailure(t *testing.T) {
	dir := t.TempDir()
	fetches := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		fetches++
		return respond(503, "")(r)
	})}
	for range 2 {
		m := GetControlManifest(t.Context(), dir, client, log.Null)
		if len(m.DisallowedFeatures) != 0 || len(m.FeatureAdvisories) != 0 {
			t.Fatalf("manifest = %+v, want empty", m)
		}
	}
	if fetches != 1 {
		t.Fatalf("fetches = %d, want 1 (the second call reads the cache)", fetches)
	}
	data, err := os.ReadFile(filepath.Join(dir, controlManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"disallowedFeatures\": [],\n  \"featureAdvisories\": []\n}"; string(data) != want {
		t.Fatalf("cache = %q, want %q", data, want)
	}
}

// A cache folder that does not exist yet is created (TS updateControlManifest
// mkdirs it) with mode 0755, the mode the CLI gives every cache folder, less
// the process umask.
func TestGetControlManifestCreatesTheCacheFolder(t *testing.T) {
	umask := processUmask(t)
	dir := filepath.Join(t.TempDir(), "cache")
	client := &http.Client{Transport: respond(200, `{"disallowedFeatures":[{"featureIdPrefix":"p"}]}`)}
	m := GetControlManifest(t.Context(), dir, client, log.Null)
	if len(m.DisallowedFeatures) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o755)&^umask; got != want {
		t.Fatalf("cache folder mode = %v, want %v", got, want)
	}
}

// processUmask reads the umask from /proc, which only Linux has.
func processUmask(t *testing.T) os.FileMode {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("no /proc/self/status: %v", err)
	}
	for line := range strings.Lines(string(data)) {
		if v, ok := strings.CutPrefix(line, "Umask:"); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 8, 32)
			if err != nil {
				t.Fatal(err)
			}
			return os.FileMode(n)
		}
	}
	t.Skip("no Umask in /proc/self/status")
	return 0
}

// An advisory whose introducedInVersion or fixedInVersion cannot be parsed
// applies to no version, as TS fetchFeatureAdvisories filters it out.
func TestFeatureAdvisoriesSkipUnparsableRanges(t *testing.T) {
	manifest := &ControlManifest{FeatureAdvisories: []Advisory{
		{FeatureID: "ghcr.io/o/f/a", IntroducedInVersion: "x", FixedInVersion: "2", Description: "no introduced"},
		{FeatureID: "ghcr.io/o/f/a", IntroducedInVersion: "1", FixedInVersion: "x", Description: "no fixed"},
	}}
	sets := []*Set{{SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: "a"}, Features: []Feature{{Version: "1.5.0"}}}}
	if got := FeatureAdvisories(manifest, sets, func(string) {}); len(got) != 0 {
		t.Fatalf("advisories = %+v, want none", got)
	}
}

// A 300 answer is outside 2xx, so it is a failed fetch worded as TS request
// words it.
func TestGetControlManifestRefusesStatus300(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(log.Options{Writer: &buf, Level: log.LevelTrace})
	m := GetControlManifest(t.Context(), t.TempDir(), &http.Client{Transport: respond(300, `{"disallowedFeatures":[{"featureIdPrefix":"p"}]}`)}, logger)
	if len(m.DisallowedFeatures) != 0 {
		t.Fatalf("manifest = %+v, want empty", m)
	}
	if !strings.Contains(buf.String(), "Failed to fetch control manifest: HTTP 300: Multiple Choices") {
		t.Fatalf("log = %q", buf.String())
	}
}
