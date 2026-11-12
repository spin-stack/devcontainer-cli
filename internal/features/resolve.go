package features

import (
	"fmt"
	"path/filepath"
	"strings"
)

type SourceType int

const (
	SourceOCI SourceType = iota
	SourceDirectTarball
	SourceLocalPath
	SourceLegacyShorthand
	SourceGitHubShorthand
)

func ClassifyID(id string) SourceType {
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
		return SourceDirectTarball
	}
	if strings.HasPrefix(id, "./") || strings.HasPrefix(id, "../") || strings.HasPrefix(id, "/") {
		return SourceLocalPath
	}
	firstSlash := strings.Index(id, "/")
	if firstSlash > 0 {
		firstSegment := id[:firstSlash]
		if strings.Contains(firstSegment, ".") || strings.HasPrefix(firstSegment, "localhost") {
			return SourceOCI
		}
		return SourceGitHubShorthand
	}
	return SourceLegacyShorthand
}

type InvalidLocalFeatureError struct{ Msg string }

func (e *InvalidLocalFeatureError) Error() string { return e.Msg }

// LocalFeaturePath resolves a local Feature reference (./x, ../x, /x) against
// configDir, the folder of the configuration that lists it, as TS
// processFeatureIdentifier does (containerFeaturesConfiguration.ts): an
// absolute reference is refused, so is one without a configuration, and the
// resolved folder must lie under workspaceRoot's .devcontainer folder - TS
// refuses any path relative to that folder that contains "..". An empty or
// relative workspaceRoot is resolved against the working directory, as TS
// resolves its process.cwd() fallback.
func LocalFeaturePath(id, configDir, workspaceRoot string) (string, error) {
	if filepath.IsAbs(id) {
		return "", &InvalidLocalFeatureError{Msg: "An Absolute path to a local feature is not allowed."}
	}
	if configDir == "" {
		return "", &InvalidLocalFeatureError{Msg: "A local feature requires a configuration path."}
	}
	featureFolderPath := filepath.Join(configDir, id)
	parent, err := filepath.Abs(filepath.Join(workspaceRoot, ".devcontainer"))
	if err != nil {
		return "", err
	}
	child, err := filepath.Abs(featureFolderPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return "", err
	}
	if strings.Contains(rel, "..") {
		return "", &InvalidLocalFeatureError{Msg: "Local file path parse error. Resolved path must be a child of the .devcontainer/ folder.  Parsed: " + featureFolderPath}
	}
	return featureFolderPath, nil
}

const versionBackwardComp = "1"

const newFeaturePath = "ghcr.io/devcontainers/features"

var migratedFeatures = map[string]struct{}{
	"aws-cli": {}, "azure-cli": {}, "desktop-lite": {},
	"docker-in-docker": {}, "docker-from-docker": {}, "dotnet": {},
	"git": {}, "git-lfs": {}, "github-cli": {}, "java": {},
	"kubectl-helm-minikube": {}, "node": {}, "powershell": {},
	"python": {}, "ruby": {}, "rust": {}, "sshd": {}, "terraform": {},
}

var renamedFeatures = map[string]string{
	"golang": "go",
	"common": "common-utils",
}

var DeprecatedFeatureIntoOptions = map[string]struct {
	MapTo  string
	Option string
}{
	"gradle":     {"java", "installGradle"},
	"maven":      {"java", "installMaven"},
	"jupyterlab": {"python", "installJupyterlab"},
}

func IsKnownLegacyFeature(name string) bool {
	if _, migrated := migratedFeatures[name]; migrated || renamedFeatures[name] != "" {
		return true
	}
	_, ok := DeprecatedFeatureIntoOptions[name]
	return ok
}

func ResolveID(id string, skipAutoMapping bool) (string, bool) {
	srcType := ClassifyID(id)

	if srcType == SourceGitHubShorthand {
		return "ghcr.io/" + id, true
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
