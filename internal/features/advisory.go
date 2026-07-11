package features

import (
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

// FeatureAdvisory describes a security advisory for a feature version range.
type FeatureAdvisory struct {
	FeatureID           string `json:"featureId"`
	IntroducedInVersion string `json:"introducedInVersion"`
	FixedInVersion      string `json:"fixedInVersion"`
	Description         string `json:"description"`
	DocumentationURL    string `json:"documentationURL,omitempty"`
}

// ControlManifest contains advisories and disallowed features from containers.dev.
type ControlManifest struct {
	DisallowedFeatures []DisallowedFeature `json:"disallowedFeatures"`
	FeatureAdvisories  []FeatureAdvisory   `json:"featureAdvisories"`
}

const (
	controlManifestURL   = "https://containers.dev/static/devcontainer-control-manifest.json"
	controlManifestFile  = "control-manifest.json"
	cacheTimeoutDuration = 5 * time.Minute
)

// GetControlManifest fetches the control manifest with 5-minute caching.
func GetControlManifest(cacheFolder string, httpClient *httpx.Client, logger log.Log) *ControlManifest {
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
	resp, err := httpClient.Do(httpx.RequestOptions{
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
func CheckAdvisories(manifest *ControlManifest, featureSets []*FeatureSet) []FeatureWithAdvisory {
	if len(manifest.FeatureAdvisories) == 0 {
		return nil
	}

	// Index advisories by feature ID
	advisoryMap := make(map[string][]FeatureAdvisory)
	for _, a := range manifest.FeatureAdvisories {
		advisoryMap[a.FeatureID] = append(advisoryMap[a.FeatureID], a)
	}

	var results []FeatureWithAdvisory
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

		var matching []FeatureAdvisory
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
			results = append(results, FeatureWithAdvisory{
				FeatureID:  featureID,
				Version:    version,
				Advisories: matching,
			})
		}
	}
	return results
}

// FeatureWithAdvisory pairs a feature with its applicable advisories.
type FeatureWithAdvisory struct {
	FeatureID  string
	Version    string
	Advisories []FeatureAdvisory
}

// EnsureNoDisallowedFeatures checks features against the blocklist.
func EnsureNoDisallowedFeatures(manifest *ControlManifest, features map[string]interface{}, additionalFeatures map[string]interface{}) error {
	if len(manifest.DisallowedFeatures) == 0 {
		return nil
	}

	check := func(featureID string) error {
		for _, d := range manifest.DisallowedFeatures {
			if strings.HasPrefix(featureID, d.FeatureIDPrefix) {
				msg := fmt.Sprintf("Feature %q is disallowed", featureID)
				if d.DocumentationURL != "" {
					msg += fmt.Sprintf(". See %s", d.DocumentationURL)
				}
				return fmt.Errorf("%s", msg)
			}
		}
		return nil
	}

	for id := range features {
		if err := check(id); err != nil {
			return err
		}
	}
	for id := range additionalFeatures {
		if err := check(id); err != nil {
			return err
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
		var advisories []FeatureAdvisory
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
		FeatureAdvisories:  []FeatureAdvisory{},
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
