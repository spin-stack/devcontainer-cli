package features

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/log"
)

// DisallowedFeature describes a blocked feature.
type DisallowedFeature struct {
	FeatureIDPrefix  string `json:"featureIdPrefix"`
	DocumentationURL string `json:"documentationURL,omitempty"`
}

// Advisory describes a security advisory for a feature version range.
type Advisory struct {
	FeatureID           string `json:"featureId"`
	IntroducedInVersion string `json:"introducedInVersion"`
	FixedInVersion      string `json:"fixedInVersion"`
	Description         string `json:"description"`
	DocumentationURL    string `json:"documentationURL,omitempty"`
}

// ControlManifest contains advisories and disallowed features from containers.dev.
type ControlManifest struct {
	DisallowedFeatures []DisallowedFeature `json:"disallowedFeatures"`
	FeatureAdvisories  []Advisory          `json:"featureAdvisories"`
}

const (
	controlManifestURL   = "https://containers.dev/static/devcontainer-control-manifest.json"
	controlManifestFile  = "control-manifest.json"
	cacheTimeoutDuration = 5 * time.Minute
)

// GetControlManifest fetches the control manifest with 5-minute caching. The
// fetch is bound to ctx so a cancelled command aborts the (network) request.
func GetControlManifest(ctx context.Context, cacheFolder string, client *http.Client, logger log.Logger) *ControlManifest {
	cachePath := filepath.Join(cacheFolder, controlManifestFile)

	// Check cache
	info, err := os.Stat(cachePath)
	if err == nil && time.Since(info.ModTime()) < cacheTimeoutDuration {
		data, err := os.ReadFile(cachePath)
		if err == nil {
			if m := sanitizeManifest(data); m != nil {
				return m
			}
		}
	}

	body, err := fetchControlManifest(ctx, client)
	if err != nil {
		logger.Write(fmt.Sprintf("Failed to fetch control manifest: %v", err), log.LevelError)
		// Return cached if available
		if data, err := os.ReadFile(cachePath); err == nil {
			if m := sanitizeManifest(data); m != nil {
				// Touch mtime to avoid flooding
				now := time.Now()
				os.Chtimes(cachePath, now, now)
				return m
			}
		}
		// TS updateControlManifest caches the empty manifest, so the fetch is
		// not retried for five minutes.
		body = []byte("{\n  \"disallowedFeatures\": [],\n  \"featureAdvisories\": []\n}")
	}

	// Write to cache atomically
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	// Written whole, then renamed over the cache, so a concurrent reader never
	// sees a partial manifest.
	if tmp, err := os.CreateTemp(filepath.Dir(cachePath), controlManifestFile+"-*"); err == nil {
		_, werr := tmp.Write(body)
		if errors.Join(werr, tmp.Close()) == nil {
			os.Rename(tmp.Name(), cachePath)
		} else {
			os.Remove(tmp.Name())
		}
	}

	if m := sanitizeManifest(body); m != nil {
		return m
	}
	return emptyManifest()
}

// fetchControlManifest GETs the manifest with TS fetchControlManifest's headers.
// A status outside 2xx is an error worded as TS request words it.
func fetchControlManifest(ctx context.Context, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, controlManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "devcontainers-vscode")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return io.ReadAll(resp.Body)
}

// FeatureAdvisories returns the OCI features with an advisory that applies to
// their version, ordered by feature id, as TS fetchFeatureAdvisories does: an
// advisory applies when introducedInVersion <= version < fixedInVersion. A
// feature whose version cannot be parsed is reported to warn and skipped.
func FeatureAdvisories(manifest *ControlManifest, featureSets []*Set, warn func(string)) []WithAdvisory {
	type feature struct{ id, version string }
	var fs []feature
	for _, set := range featureSets {
		src, ok := set.SourceInfo.(*OCISource)
		if !ok || len(set.Features) == 0 {
			continue
		}
		fs = append(fs, feature{src.Registry + "/" + src.Namespace + "/" + src.ID, set.Features[0].Version})
	}
	slices.SortStableFunc(fs, func(a, b feature) int { return strings.Compare(a.id, b.id) })
	if len(manifest.FeatureAdvisories) == 0 {
		return nil
	}
	byID := make(map[string][]Advisory)
	for _, a := range manifest.FeatureAdvisories {
		byID[a.FeatureID] = append(byID[a.FeatureID], a)
	}
	var results []WithAdvisory
	for _, f := range fs {
		version := parseVersion(f.version)
		if version == nil {
			warn(fmt.Sprintf("Unable to parse version for feature %s: %s", f.id, f.version))
			continue
		}
		var matching []Advisory
		for _, a := range byID[f.id] {
			introduced, fixed := parseVersion(a.IntroducedInVersion), parseVersion(a.FixedInVersion)
			if introduced != nil && fixed != nil && !isEarlierVersion(version, introduced) && isEarlierVersion(version, fixed) {
				matching = append(matching, a)
			}
		}
		if len(matching) > 0 {
			results = append(results, WithAdvisory{FeatureID: f.id, Version: f.version, Advisories: matching})
		}
	}
	return results
}

// FormatFeatureAdvisories is the warning TS logFeatureAdvisories writes, or ""
// when there is none.
func FormatFeatureAdvisories(list []WithAdvisory) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n-----------------------------------------------------------------------------------------------------------\nFEATURE ADVISORIES:")
	for _, f := range list {
		fmt.Fprintf(&b, "\n- %s:%s:", f.FeatureID, f.Version)
		for _, a := range f.Advisories {
			fmt.Fprintf(&b, "\n  - %s (introduced in %s, fixed in %s", a.Description, a.IntroducedInVersion, a.FixedInVersion)
			if a.DocumentationURL != "" {
				fmt.Fprintf(&b, ", see %s", a.DocumentationURL)
			}
			b.WriteString(")")
		}
	}
	b.WriteString("\n\nIt is recommended that you update your configuration to versions of these features with the fixes applied.\n-----------------------------------------------------------------------------------------------------------\n\n")
	return b.String()
}

// WithAdvisory pairs a feature with its applicable advisories.
type WithAdvisory struct {
	FeatureID  string
	Version    string
	Advisories []Advisory
}

// DisallowedFeatureError reports a config feature that the control manifest
// blocklists. The CLI layer turns it into the user-facing ContainerError envelope.
type DisallowedFeatureError struct {
	FeatureID        string
	DocumentationURL string
}

func (e *DisallowedFeatureError) Error() string {
	return fmt.Sprintf("Cannot use the '%s' Feature since it was reported to be problematic.", e.FeatureID)
}

// findDisallowedFeatureEntry matches a feature id against the blocklist. Like the
// TS CLI, a prefix only matches when the id equals the prefix or continues with a
// separator (/ : @) — so "foo" does not block "foobar".
func findDisallowedFeatureEntry(manifest *ControlManifest, featureID string) *DisallowedFeature {
	for i := range manifest.DisallowedFeatures {
		d := &manifest.DisallowedFeatures[i]
		if !strings.HasPrefix(featureID, d.FeatureIDPrefix) {
			continue
		}
		if len(featureID) == len(d.FeatureIDPrefix) {
			return d
		}
		switch featureID[len(d.FeatureIDPrefix)] {
		case '/', ':', '@':
			return d
		}
	}
	return nil
}

// EnsureNoDisallowedFeatures returns a *DisallowedFeatureError for the first of
// featureIDs - the config's features then the additional ones, in the order the
// JSON lists them, as TS Object.keys({...config.features, ...additionalFeatures})
// - that the control manifest disallows.
func EnsureNoDisallowedFeatures(manifest *ControlManifest, featureIDs []string) error {
	for _, id := range featureIDs {
		if d := findDisallowedFeatureEntry(manifest, id); d != nil {
			return &DisallowedFeatureError{FeatureID: id, DocumentationURL: d.DocumentationURL}
		}
	}
	return nil
}

// --- Helpers ---

func sanitizeManifest(data []byte) *ControlManifest {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}

	m := emptyManifest()

	if d, ok := raw["disallowedFeatures"]; ok {
		var features []DisallowedFeature
		if json.Unmarshal(d, &features) == nil {
			for _, f := range features {
				if f.FeatureIDPrefix != "" {
					m.DisallowedFeatures = append(m.DisallowedFeatures, f)
				}
			}
		}
	}

	if a, ok := raw["featureAdvisories"]; ok {
		var advisories []Advisory
		if json.Unmarshal(a, &advisories) == nil {
			for _, adv := range advisories {
				if adv.FeatureID != "" && adv.IntroducedInVersion != "" && adv.FixedInVersion != "" && adv.Description != "" {
					m.FeatureAdvisories = append(m.FeatureAdvisories, adv)
				}
			}
		}
	}

	return m
}

func emptyManifest() *ControlManifest {
	return &ControlManifest{
		DisallowedFeatures: []DisallowedFeature{},
		FeatureAdvisories:  []Advisory{},
	}
}

// versionPrefix is TS commonUtils.parseVersion's: an optional quote and v, then
// dot-separated numbers; anything after them is ignored ("1.2.3-rc" is 1.2.3).
var versionPrefix = regexp.MustCompile(`^'?v?(\d+(\.\d+)*)`)

// parseVersion is TS commonUtils.parseVersion: nil when s starts with no version.
func parseVersion(s string) []int {
	m := versionPrefix.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	var out []int
	for p := range strings.SplitSeq(m[1], ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// isEarlierVersion is TS commonUtils.isEarlierVersion: a missing component is 0,
// so 1 and 1.0 are equal and neither is earlier.
func isEarlierVersion(left, right []int) bool {
	for i := range max(len(left), len(right)) {
		var l, r int
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l != r {
			return l < r
		}
	}
	return false
}
