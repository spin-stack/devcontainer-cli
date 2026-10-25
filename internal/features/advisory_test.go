package features

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/log"
)

// TS featureAdvisories.test.ts: one advisory on feature-with-advisory, introduced
// in 1.0.7 and fixed in 1.1.10, matches the versions in [1.0.7, 1.1.10) and no
// other feature.
func TestFeatureAdvisoriesMatchTheVersionRange(t *testing.T) {
	const featureID = "ghcr.io/devcontainers/features/feature-with-advisory"
	manifest := &ControlManifest{FeatureAdvisories: []Advisory{{
		FeatureID: featureID, IntroducedInVersion: "1.0.7", FixedInVersion: "1.1.10", Description: "issue",
	}}}
	set := func(id, version string) []*Set {
		return []*Set{{
			SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "devcontainers/features", ID: id},
			Features:   []Feature{{Version: version}},
		}}
	}
	for _, tt := range []struct {
		id, version string
		want        bool
	}{
		{"feature-with-advisory", "1.0.9", true},
		{"feature-with-advisory", "1.1.5", true},
		{"feature-with-advisory", "1.0.7", true},
		{"feature-with-advisory", "1.1.9", true},
		{"feature-with-advisory", "1.0.2", false},
		{"feature-with-advisory", "1.2.1", false},
		{"feature-with-advisory", "1.0.6", false},
		{"feature-with-advisory", "1.1.10", false},
		{"other-feature", "1.0.9", false},
	} {
		got := FeatureAdvisories(manifest, set(tt.id, tt.version), func(string) {})
		if (len(got) == 1) != tt.want {
			t.Errorf("%s@%s: advisories = %+v, want match=%v", tt.id, tt.version, got, tt.want)
			continue
		}
		if tt.want && (got[0].FeatureID != featureID || got[0].Version != tt.version || len(got[0].Advisories) != 1) {
			t.Errorf("%s@%s: got %+v", tt.id, tt.version, got[0])
		}
	}
}

// Features are reported in id order, and one whose version cannot be parsed is
// warned about, as TS fetchFeatureAdvisories does; the warning text is TS
// logFeatureAdvisories' byte for byte.
func TestFeatureAdvisoriesOrderWarningAndText(t *testing.T) {
	manifest := &ControlManifest{FeatureAdvisories: []Advisory{
		{FeatureID: "ghcr.io/o/f/b", IntroducedInVersion: "1", FixedInVersion: "2", Description: "b broke", DocumentationURL: "https://b"},
		{FeatureID: "ghcr.io/o/f/a", IntroducedInVersion: "1", FixedInVersion: "2", Description: "a broke"},
	}}
	sets := []*Set{
		{SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: "b"}, Features: []Feature{{Version: "1.5.0"}}},
		{SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: "c"}, Features: []Feature{{Version: "latest"}}},
		{SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: "a"}, Features: []Feature{{Version: "1.0"}}},
		{SourceInfo: &LocalSource{}, Features: []Feature{{Version: "1.0"}}},
	}
	var warnings []string
	got := FeatureAdvisories(manifest, sets, func(s string) { warnings = append(warnings, s) })
	if len(got) != 2 || got[0].FeatureID != "ghcr.io/o/f/a" || got[1].FeatureID != "ghcr.io/o/f/b" {
		t.Fatalf("got %+v, want a then b", got)
	}
	if len(warnings) != 1 || warnings[0] != "Unable to parse version for feature ghcr.io/o/f/c: latest" {
		t.Errorf("warnings = %q", warnings)
	}
	want := "\n\n-----------------------------------------------------------------------------------------------------------\n" +
		"FEATURE ADVISORIES:\n" +
		"- ghcr.io/o/f/a:1.0:\n  - a broke (introduced in 1, fixed in 2)\n" +
		"- ghcr.io/o/f/b:1.5.0:\n  - b broke (introduced in 1, fixed in 2, see https://b)\n\n" +
		"It is recommended that you update your configuration to versions of these features with the fixes applied.\n" +
		"-----------------------------------------------------------------------------------------------------------\n\n"
	if text := FormatFeatureAdvisories(got); text != want {
		t.Errorf("text:\n%q\nwant:\n%q", text, want)
	}
	if FormatFeatureAdvisories(nil) != "" {
		t.Error("no advisories writes nothing")
	}
}

func TestEnsureNoDisallowedFeatures(t *testing.T) {
	const docURL = "https://example.com"
	// A prefix only matches on a separator boundary (/ : @) or exact length, like TS,
	// so it must not block a feature that merely shares the prefix's characters. When
	// blocked, the returned *DisallowedFeatureError carries the id and doc URL.
	tests := []struct {
		name        string
		prefix      string
		docURL      string
		featureID   string
		wantBlocked bool
	}{
		{"unrelated feature allowed", "ghcr.io/bad/", "", "ghcr.io/good/features/go:1", false},
		{"exact-prefix version blocked", "ghcr.io/bad/features/evil", docURL, "ghcr.io/bad/features/evil:1", true},
		{"boundary: exact match", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool", true},
		{"boundary: ':' separator", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool:1", true},
		{"boundary: '@' separator", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tool@sha256:x", true},
		{"boundary: shared chars not blocked", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/toolkit:1", false},
		{"boundary: suffix not blocked", "ghcr.io/acme/features/tool", "", "ghcr.io/acme/features/tooling", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &ControlManifest{DisallowedFeatures: []DisallowedFeature{
				{FeatureIDPrefix: tt.prefix, DocumentationURL: tt.docURL},
			}}
			err := EnsureNoDisallowedFeatures(manifest, []string{tt.featureID})

			if (err != nil) != tt.wantBlocked {
				t.Fatalf("blocked=%v, want %v (err=%v)", err != nil, tt.wantBlocked, err)
			}
			if !tt.wantBlocked {
				return
			}
			var dfe *DisallowedFeatureError
			if !errors.As(err, &dfe) {
				t.Fatalf("expected *DisallowedFeatureError, got %T", err)
			}
			if dfe.FeatureID != tt.featureID || dfe.DocumentationURL != tt.docURL {
				t.Errorf("error fields = {%q, %q}, want {%q, %q}", dfe.FeatureID, dfe.DocumentationURL, tt.featureID, tt.docURL)
			}
		})
	}
}

// roundTripFunc answers requests in-process, so the control manifest fetch is
// tested without the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(code int, body string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
}

// A non-2xx answer is logged as TS logs it ("HTTP 503: Service Unavailable"),
// and the cached manifest, however old, is kept with its mtime refreshed.
func TestGetControlManifestFailedFetch(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, controlManifestFile)
	if err := os.WriteFile(cache, []byte(`{"disallowedFeatures":[{"featureIdPrefix":"old"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cache, stale, stale); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := log.New(log.Options{Writer: &buf, Level: log.LevelTrace})

	m := GetControlManifest(t.Context(), dir, &http.Client{Transport: respond(503, "")}, logger)
	if !strings.Contains(buf.String(), "Failed to fetch control manifest: HTTP 503: Service Unavailable") {
		t.Fatalf("log = %q", buf.String())
	}
	if len(m.DisallowedFeatures) != 1 || m.DisallowedFeatures[0].FeatureIDPrefix != "old" {
		t.Fatalf("manifest = %+v, want the cached one", m)
	}
	if info, err := os.Stat(cache); err != nil || !info.ModTime().After(stale) {
		t.Fatalf("cache mtime not refreshed: %v", err)
	}

	// Without a cache the result is the empty manifest.
	m = GetControlManifest(t.Context(), t.TempDir(), &http.Client{Transport: respond(404, "")}, logger)
	if len(m.DisallowedFeatures) != 0 || len(m.FeatureAdvisories) != 0 {
		t.Fatalf("manifest = %+v, want empty", m)
	}
}

// A fresh fetch sends TS's headers, is cached, and is what is returned.
func TestGetControlManifestFetchesAndCaches(t *testing.T) {
	dir := t.TempDir()
	var got http.Header
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header
		return respond(200, `{"featureAdvisories":[{"featureId":"x","introducedInVersion":"1.0.0","fixedInVersion":"1.1.0","description":"d"}]}`)(r)
	})}
	m := GetControlManifest(t.Context(), dir, client, log.Null)
	if len(m.FeatureAdvisories) != 1 || m.FeatureAdvisories[0].FeatureID != "x" {
		t.Fatalf("manifest = %+v", m)
	}
	if got.Get("User-Agent") != "devcontainers-vscode" || got.Get("Accept") != "application/json" {
		t.Fatalf("headers = %v", got)
	}
	if data, err := os.ReadFile(filepath.Join(dir, controlManifestFile)); err != nil || !strings.Contains(string(data), `"x"`) {
		t.Fatalf("cache = %q, %v", data, err)
	}
}
