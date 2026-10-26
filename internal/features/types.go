// Package features resolves, orders, installs and locks dev container Features.
package features

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/devcontainers/cli/internal/jsonc"
)

// Set represents a resolved feature with its source and metadata.
type Set struct {
	SourceInfo      SourceInfo `json:"sourceInformation"`
	Features        []Feature  `json:"features"`
	ComputedDigest  string     `json:"computedDigest,omitempty"`
	InternalVersion string     `json:"internalVersion,omitempty"`
}

// Feature represents a single feature within a Set.
type Feature struct {
	ID               string         `json:"id"`
	Version          string         `json:"version,omitempty"`
	Name             string         `json:"name,omitempty"`
	Description      string         `json:"description,omitempty"`
	DocumentationURL string         `json:"documentationURL,omitempty"`
	Options          map[string]any `json:"options,omitempty"`
	DependsOn        map[string]any `json:"dependsOn,omitempty"`
	// DependsOnOrder is the order the metadata lists dependsOn's keys in,
	// which TS keeps (Object.keys); UnmarshalJSON records it.
	DependsOnOrder       []string          `json:"-"`
	InstallsAfter        []string          `json:"installsAfter,omitempty"`
	LegacyIds            []string          `json:"legacyIds,omitempty"`
	Included             bool              `json:"included"`
	CachePath            string            `json:"cachePath,omitempty"`
	ConsecutiveId        string            `json:"consecutiveId,omitempty"`
	Value                any               `json:"value"` // normalized main value
	UserOptions          map[string]any    `json:"-"`     // original user option values from config
	ContainerEnv         map[string]string `json:"containerEnv,omitempty"`
	Mounts               []any             `json:"mounts,omitempty"`
	Init                 *bool             `json:"init,omitempty"`
	Privileged           *bool             `json:"privileged,omitempty"`
	CapAdd               []string          `json:"capAdd,omitempty"`
	SecurityOpt          []string          `json:"securityOpt,omitempty"`
	Entrypoint           string            `json:"entrypoint,omitempty"`
	OnCreateCommand      any               `json:"onCreateCommand,omitempty"`
	UpdateContentCommand any               `json:"updateContentCommand,omitempty"`
	PostCreateCommand    any               `json:"postCreateCommand,omitempty"`
	PostStartCommand     any               `json:"postStartCommand,omitempty"`
	PostAttachCommand    any               `json:"postAttachCommand,omitempty"`
	Customizations       map[string]any    `json:"customizations,omitempty"`
}

// UnmarshalJSON decodes the Feature and records the order of its dependsOn
// keys in DependsOnOrder.
func (f *Feature) UnmarshalJSON(data []byte) error {
	type plain Feature
	if err := json.Unmarshal(data, (*plain)(f)); err != nil {
		return err
	}
	f.DependsOnOrder = jsonc.MemberNames(data, "dependsOn")
	return nil
}

// DependsOnKeys is TS Object.keys(dependsOn) for a Feature decoded from its
// metadata: the keys of DependsOn in the order DependsOnOrder lists them (a
// repeated key where it first appears). Keys DependsOnOrder does not list,
// as for a Feature built in code, follow in sorted order.
func (f *Feature) DependsOnKeys() []string {
	keys := make([]string, 0, len(f.DependsOn))
	listed := make(map[string]bool, len(f.DependsOn))
	for _, k := range f.DependsOnOrder {
		if _, ok := f.DependsOn[k]; ok && !listed[k] {
			listed[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(f.DependsOn)) {
		if !listed[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

// SourceInfo describes where a feature comes from.
type SourceInfo interface {
	SourceType() string
	UserFeatureID() string
}

// OCISource is a feature from an OCI registry.
type OCISource struct {
	Type                        string `json:"type"`
	ManifestDigest              string `json:"manifestDigest,omitempty"`
	UserID                      string `json:"userFeatureId"`
	UserFeatureIdWithoutVersion string `json:"userFeatureIdWithoutVersion,omitempty"`
	FeatureRef                  any    `json:"featureRef,omitempty"`
	Manifest                    any    `json:"manifest,omitempty"`
	// Internal fields not serialized to JSON (used for dependency resolution)
	Registry  string `json:"-"`
	Namespace string `json:"-"`
	ID        string `json:"-"`
	Resource  string `json:"-"`
	Tag       string `json:"-"`
}

func (s *OCISource) SourceType() string    { return "oci" }
func (s *OCISource) UserFeatureID() string { return s.UserID }

// TarballSource is a feature from a direct HTTP tarball URL.
type TarballSource struct {
	TarballURI string
	UserID     string
}

func (s *TarballSource) SourceType() string    { return "direct-tarball" }
func (s *TarballSource) UserFeatureID() string { return s.UserID }

// MarshalJSON writes TS DirectTarballSourceInformation, as read-configuration
// reports it in featuresConfiguration.
func (s *TarballSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type          string `json:"type"`
		TarballURI    string `json:"tarballUri"`
		UserFeatureID string `json:"userFeatureId"`
	}{s.SourceType(), s.TarballURI, s.UserID})
}

// LocalSource is a feature from a local file path.
type LocalSource struct {
	LocalPath    string
	ResolvedPath string
	UserID       string
}

func (s *LocalSource) SourceType() string    { return "file-path" }
func (s *LocalSource) UserFeatureID() string { return s.UserID }

// MarshalJSON writes TS FilePathSourceInformation, as read-configuration
// reports it in featuresConfiguration.
func (s *LocalSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		ResolvedFilePath string `json:"resolvedFilePath"`
		UserFeatureID    string `json:"userFeatureId"`
	}{s.SourceType(), s.ResolvedPath, s.UserID})
}

// Config holds the fully resolved features configuration.
type Config struct {
	FeatureSets []*Set
}

// DevContainerFeature is a user's feature reference from devcontainer.json.
type DevContainerFeature struct {
	UserFeatureID string
	Options       any // bool | string | map[string]interface{}
}

// UserFeaturesToArray converts the features map from devcontainer.json
// into a slice of DevContainerFeature.
func UserFeaturesToArray(features map[string]any) []DevContainerFeature {
	if features == nil {
		return nil
	}
	result := make([]DevContainerFeature, 0, len(features))
	for id, opts := range features {
		result = append(result, DevContainerFeature{
			UserFeatureID: id,
			Options:       opts,
		})
	}
	return result
}
