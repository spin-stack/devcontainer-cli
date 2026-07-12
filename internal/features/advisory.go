package features

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/httpx"
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
func GetControlManifest(ctx context.Context, cacheFolder string, httpClient *httpx.Client, logger log.Logger) *ControlManifest {
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

	// Fetch fresh
	resp, err := httpClient.Do(ctx, httpx.RequestOptions{
		URL: controlManifestURL,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})
	if err != nil || resp.StatusCode > 299 {
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
		return emptyManifest()
	}

	// Write to cache atomically
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	tmpName := cachePath + "-" + randomHex(8)
	os.WriteFile(tmpName, resp.Body, 0644)
	os.Rename(tmpName, cachePath)

	if m := sanitizeManifest(resp.Body); m != nil {
		return m
	}
	return emptyManifest()
}

// CheckAdvisories returns features that have active advisories.
func CheckAdvisories(manifest *ControlManifest, featureSets []*Set) []WithAdvisory {
	if len(manifest.FeatureAdvisories) == 0 {
		return nil
	}

	// Index advisories by feature ID
	advisoryMap := make(map[string][]Advisory)
	for _, a := range manifest.FeatureAdvisories {
		advisoryMap[a.FeatureID] = append(advisoryMap[a.FeatureID], a)
	}

	var results []WithAdvisory
	for _, fs := range featureSets {
		ociSrc, ok := fs.SourceInfo.(*OCISource)
		if !ok || len(fs.Features) == 0 {
			continue
		}

		featureID := fmt.Sprintf("%s/%s/%s", ociSrc.Registry, ociSrc.Namespace, ociSrc.ID)
		version := fs.Features[0].Version

		advisories, ok := advisoryMap[featureID]
		if !ok {
			continue
		}

		featureVer := parseVersion(version)
		if featureVer == nil {
			continue
		}

		var matching []Advisory
		for _, a := range advisories {
			introducedVer := parseVersion(a.IntroducedInVersion)
			fixedVer := parseVersion(a.FixedInVersion)
			if introducedVer == nil || fixedVer == nil {
				continue
			}
			// Advisory applies if: introduced <= featureVer < fixed
			if !isEarlierVersion(featureVer, introducedVer) && isEarlierVersion(featureVer, fixedVer) {
				matching = append(matching, a)
			}
		}

		if len(matching) > 0 {
			results = append(results, WithAdvisory{
				FeatureID:  featureID,
				Version:    version,
				Advisories: matching,
			})
		}
	}
	return results
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

// EnsureNoDisallowedFeatures checks config + additional features against the
// control-manifest blocklist and returns a *DisallowedFeatureError for the first
// disallowed one (nil if all are allowed).
func EnsureNoDisallowedFeatures(manifest *ControlManifest, features map[string]interface{}, additionalFeatures map[string]interface{}) error {
	if len(manifest.DisallowedFeatures) == 0 {
		return nil
	}
	for id := range features {
		if d := findDisallowedFeatureEntry(manifest, id); d != nil {
			return &DisallowedFeatureError{FeatureID: id, DocumentationURL: d.DocumentationURL}
		}
	}
	for id := range additionalFeatures {
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

// parseVersion parses "1.2.3" into [1, 2, 3].
func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	result := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		result = append(result, n)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// isEarlierVersion returns true if a < b (component-wise).
func isEarlierVersion(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return len(a) < len(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
