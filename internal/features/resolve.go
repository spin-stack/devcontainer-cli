package features

import (
	"fmt"
	"strings"
)

// SourceType identifies how a feature ID should be resolved.
type SourceType int

const (
	SourceOCI SourceType = iota
	SourceDirectTarball
	SourceLocalPath
	SourceLegacyShorthand
	SourceGitHubShorthand // namespace/feature without domain (e.g., codspace/myfeatures/helloworld)
)

// ClassifyID determines the source type of a feature identifier.
// Matches TS processFeatureIdentifier() routing logic.
func ClassifyID(id string) SourceType {
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
		return SourceDirectTarball
	}
	if strings.HasPrefix(id, "./") || strings.HasPrefix(id, "../") {
		return SourceLocalPath
	}
	// If it contains a dot in the first segment (before first /), it's a domain → OCI
	firstSlash := strings.Index(id, "/")
	if firstSlash > 0 {
		firstSegment := id[:firstSlash]
		if strings.Contains(firstSegment, ".") || strings.HasPrefix(firstSegment, "localhost") {
			return SourceOCI
		}
		// Has slashes but no domain → GitHub shorthand (e.g., codspace/myfeatures/helloworld)
		// These resolve to ghcr.io/{id}
		return SourceGitHubShorthand
	}
	// No slashes → single-word legacy shorthand (e.g., "go", "node")
	return SourceLegacyShorthand
}

// versionBackwardComp is the version tag pinned for auto-mapped legacy
// features (TS getBackwardCompatibleFeatureId), so they resolve to a known
// major instead of `latest`.
const versionBackwardComp = "1"

const newFeaturePath = "ghcr.io/devcontainers/features"

// migratedFeatures are legacy shorthands that map 1:1 to the same id under the
// new registry path (with the :1 pin). Matches the TS migratedfeatures list.
var migratedFeatures = map[string]struct{}{
	"aws-cli": {}, "azure-cli": {}, "desktop-lite": {},
	"docker-in-docker": {}, "docker-from-docker": {}, "dotnet": {},
	"git": {}, "git-lfs": {}, "github-cli": {}, "java": {},
	"kubectl-helm-minikube": {}, "node": {}, "powershell": {},
	"python": {}, "ruby": {}, "rust": {}, "sshd": {}, "terraform": {},
}

// renamedFeatures maps a legacy shorthand to its renamed id (TS renamedFeatures).
var renamedFeatures = map[string]string{
	"golang": "go",
	"common": "common-utils",
}

// DeprecatedFeatureIntoOptions maps a legacy shorthand that became an option of
// another Feature to (target feature, option flag). gradle/maven fold into java,
// jupyterlab into python. Matches the TS deprecatedFeaturesIntoOptions.
var DeprecatedFeatureIntoOptions = map[string]struct {
	MapTo  string
	Option string
}{
	"gradle":     {"java", "installGradle"},
	"maven":      {"java", "installMaven"},
	"jupyterlab": {"python", "installJupyterlab"},
}

// IsKnownLegacyFeature reports whether a legacy shorthand name is one the CLI
// auto-maps (migrated 1:1, renamed, or folded into another Feature's options).
func IsKnownLegacyFeature(name string) bool {
	if _, migrated := migratedFeatures[name]; migrated || renamedFeatures[name] != "" {
		return true
	}
	_, ok := DeprecatedFeatureIntoOptions[name]
	return ok
}

// ResolveID resolves a user-provided feature ID to its canonical OCI reference.
// For legacy shorthands, applies the deprecated feature map.
// Returns the resolved ID and whether auto-mapping was applied.
func ResolveID(id string, skipAutoMapping bool) (string, bool) {
	srcType := ClassifyID(id)

	// GitHub shorthand: namespace/feature → ghcr.io/namespace/feature
	if srcType == SourceGitHubShorthand {
		// Strip version for resolution
		name := id
		version := ""
		if idx := strings.LastIndex(id, ":"); idx > 0 {
			name = id[:idx]
			version = id[idx:]
		}
		return "ghcr.io/" + name + version, true
	}

	if srcType != SourceLegacyShorthand {
		return id, false
	}

	if skipAutoMapping {
		return id, false
	}

	// Split the name from an explicit version tag. Auto-mapped legacy features
	// are pinned to versionBackwardComp (:1) unless the user gave an explicit
	// version — matching the TS getBackwardCompatibleFeatureId (which pins :1),
	// while still honoring an explicit tag if provided.
	name := id
	tag := ":" + versionBackwardComp
	if idx := strings.LastIndex(id, ":"); idx > 0 {
		name = id[:idx]
		tag = id[idx:]
	}

	target := name
	if renamed, ok := renamedFeatures[name]; ok {
		target = renamed
	}
	return fmt.Sprintf("%s/%s%s", newFeaturePath, target, tag), true
}

// StripVersionFromID removes the version portion (:tag or @digest) from a feature ID.
func StripVersionFromID(id string) string {
	// Check for digest first
	if idx := strings.LastIndex(id, "@"); idx > 0 {
		return id[:idx]
	}
	// Then tag (colon after last slash)
	lastSlash := strings.LastIndex(id, "/")
	lastColon := strings.LastIndex(id, ":")
	if lastColon > lastSlash {
		return id[:lastColon]
	}
	return id
}
