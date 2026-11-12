package features

import (
	"bytes"
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

type DisallowedFeature struct {
	FeatureIDPrefix  string `json:"featureIdPrefix"`
	DocumentationURL string `json:"documentationURL,omitempty"`
}

type Advisory struct {
	FeatureID           string `json:"featureId"`
	IntroducedInVersion string `json:"introducedInVersion"`
	FixedInVersion      string `json:"fixedInVersion"`
	Description         string `json:"description"`
	DocumentationURL    string `json:"documentationURL,omitempty"`
}

type ControlManifest struct {
	DisallowedFeatures []DisallowedFeature `json:"disallowedFeatures"`
	FeatureAdvisories  []Advisory          `json:"featureAdvisories"`
}

const (
	controlManifestURL   = "https://containers.dev/static/devcontainer-control-manifest.json"
	controlManifestFile  = "control-manifest.json"
	cacheTimeoutDuration = 5 * time.Minute
)

func GetControlManifest(ctx context.Context, cacheFolder string, client *http.Client, logger log.Logger) *ControlManifest {
	cachePath := filepath.Join(cacheFolder, controlManifestFile)

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
		if data, err := os.ReadFile(cachePath); err == nil {
			if m := sanitizeManifest(data); m != nil {
				now := time.Now()
				os.Chtimes(cachePath, now, now)
				return m
			}
		}
		// TS updateControlManifest caches the empty manifest, so the fetch is
		// not retried for five minutes.
		body = []byte("{\n  \"disallowedFeatures\": [],\n  \"featureAdvisories\": []\n}")
	}

	os.MkdirAll(filepath.Dir(cachePath), 0755)
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

type WithAdvisory struct {
	FeatureID  string
	Version    string
	Advisories []Advisory
}

type DisallowedFeatureError struct {
	FeatureID        string
	DocumentationURL string
}

func (e *DisallowedFeatureError) Error() string {
	return fmt.Sprintf("Cannot use the '%s' Feature since it was reported to be problematic.", e.FeatureID)
}

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

func EnsureNoDisallowedFeatures(manifest *ControlManifest, featureIDs []string) error {
	for _, id := range featureIDs {
		if d := findDisallowedFeatureEntry(manifest, id); d != nil {
			return &DisallowedFeatureError{FeatureID: id, DocumentationURL: d.DocumentationURL}
		}
	}
	return nil
}

func sanitizeManifest(data []byte) *ControlManifest {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}

	m := emptyManifest()
	for _, elem := range manifestEntries(raw["disallowedFeatures"], "featureIdPrefix") {
		var f DisallowedFeature
		_ = json.Unmarshal(elem, &f)
		m.DisallowedFeatures = append(m.DisallowedFeatures, f)
	}
	for _, elem := range manifestEntries(raw["featureAdvisories"], "featureId", "introducedInVersion", "fixedInVersion", "description") {
		var adv Advisory
		_ = json.Unmarshal(elem, &adv)
		m.FeatureAdvisories = append(m.FeatureAdvisories, adv)
	}
	return m
}

func manifestEntries(list json.RawMessage, fields ...string) []json.RawMessage {
	var elems []json.RawMessage
	_ = json.Unmarshal(list, &elems)
	var kept []json.RawMessage
	for _, elem := range elems {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(elem, &obj)
		if !slices.ContainsFunc(fields, func(field string) bool { return !isJSONString(obj[field]) }) {
			kept = append(kept, elem)
		}
	}
	return kept
}

// isJSONString reports whether value, an object field as encoding/json
// decodes it (without the whitespace around it), is a JSON string.
func isJSONString(value json.RawMessage) bool {
	return bytes.HasPrefix(value, []byte(`"`))
}

func emptyManifest() *ControlManifest {
	return &ControlManifest{
		DisallowedFeatures: []DisallowedFeature{},
		FeatureAdvisories:  []Advisory{},
	}
}

var versionPrefix = regexp.MustCompile(`^'?v?(\d+(\.\d+)*)`)

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
