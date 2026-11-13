package features

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

type SourceType int

const (
	SourceOCI SourceType = iota
	SourceDirectTarball
	SourceLocalPath
	SourceLegacyShorthand
)

func ClassifyID(id string) SourceType {
	if !strings.ContainsAny(id, `/\`) {
		return SourceLegacyShorthand
	}
	// TS takes only https:// as a tarball URL.
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
		return SourceDirectTarball
	}
	if strings.HasPrefix(id, "./") || strings.HasPrefix(id, "../") || strings.HasPrefix(id, "/") {
		return SourceLocalPath
	}
	return SourceOCI
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

var deprecatedFeaturesIntoOptions = map[string]struct {
	mapTo  string
	option string
}{
	"gradle":     {"java", "installGradle"},
	"maven":      {"java", "installMaven"},
	"jupyterlab": {"python", "installJupyterlab"},
}

func BackwardCompatibleID(id string) (mapped, warning string) {
	if _, ok := migratedFeatures[id]; ok {
		return newFeaturePath + "/" + id + ":1", deprecatedFeatureWarning(id, id)
	}
	if to, ok := renamedFeatures[id]; ok {
		return newFeaturePath + "/" + to + ":1", deprecatedFeatureWarning(id, to)
	}
	if m, ok := deprecatedFeaturesIntoOptions[id]; ok {
		return id, fmt.Sprintf("(!) WARNING: Falling back to the deprecated '%s' Feature. It is now part of the '%s' Feature. See https://github.com/devcontainers/features/tree/main/src/%s#options for the updated Feature.", id, m.mapTo, m.mapTo)
	}
	return id, ""
}

func deprecatedFeatureWarning(id, src string) string {
	return fmt.Sprintf("(!) WARNING: Using the deprecated '%s' Feature. See https://github.com/devcontainers/features/tree/main/src/%s#example-usage for the updated Feature.", id, src)
}

func UpdateDeprecatedFeaturesIntoOptions(userFeatures []DevContainerFeature) (updated []DevContainerFeature, warnings []string) {
	updated = slices.Clone(userFeatures)
	for _, f := range userFeatures {
		m, ok := deprecatedFeaturesIntoOptions[f.UserFeatureID]
		if !ok {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("(!) WARNING: Using the deprecated '%s' Feature. It is now part of the '%s' Feature. See https://github.com/devcontainers/features/tree/main/src/%s#options for the updated Feature.", f.UserFeatureID, m.mapTo, m.mapTo))
		qualified := newFeaturePath + "/" + m.mapTo
		i := slices.IndexFunc(updated, func(u DevContainerFeature) bool {
			return u.UserFeatureID == m.mapTo || u.UserFeatureID == qualified || strings.HasPrefix(u.UserFeatureID, qualified+":")
		})
		if i == -1 {
			updated = append(updated, DevContainerFeature{UserFeatureID: qualified + ":1", Options: map[string]any{m.option: true}, OptionOrder: []string{m.option}})
			continue
		}
		options := map[string]any{}
		order := slices.Clone(updated[i].OptionOrder)
		switch o := updated[i].Options.(type) {
		case map[string]any:
			maps.Copy(options, o)
		case string:
			options["version"] = o
			order = []string{"version"}
		}
		options[m.option] = true
		if !slices.Contains(order, m.option) {
			order = append(order, m.option)
		}
		updated[i].Options = options
		updated[i].OptionOrder = order
	}
	updated = slices.DeleteFunc(updated, func(u DevContainerFeature) bool {
		_, deprecated := deprecatedFeaturesIntoOptions[u.UserFeatureID]
		return deprecated
	})
	return updated, warnings
}
