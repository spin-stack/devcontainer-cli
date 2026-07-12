// Package features resolves, orders, installs and locks dev container Features.
package features

// Set represents a resolved feature with its source and metadata.
type Set struct {
	SourceInfo      SourceInfo `json:"sourceInformation"`
	Features        []Feature  `json:"features"`
	ComputedDigest  string     `json:"computedDigest,omitempty"`
	InternalVersion string     `json:"internalVersion,omitempty"`
}

// Feature represents a single feature within a Set.
type Feature struct {
	ID                   string                 `json:"id"`
	Version              string                 `json:"version,omitempty"`
	Name                 string                 `json:"name,omitempty"`
	Description          string                 `json:"description,omitempty"`
	DocumentationURL     string                 `json:"documentationURL,omitempty"`
	Options              map[string]interface{} `json:"options,omitempty"`
	DependsOn            map[string]interface{} `json:"dependsOn,omitempty"`
	InstallsAfter        []string               `json:"installsAfter,omitempty"`
	LegacyIds            []string               `json:"legacyIds,omitempty"`
	Included             bool                   `json:"included"`
	CachePath            string                 `json:"cachePath,omitempty"`
	ConsecutiveId        string                 `json:"consecutiveId,omitempty"`
	Value                interface{}            `json:"value"` // normalized main value
	UserOptions          map[string]interface{} `json:"-"`     // original user option values from config
	ContainerEnv         map[string]string      `json:"containerEnv,omitempty"`
	Mounts               []interface{}          `json:"mounts,omitempty"`
	Init                 *bool                  `json:"init,omitempty"`
	Privileged           *bool                  `json:"privileged,omitempty"`
	CapAdd               []string               `json:"capAdd,omitempty"`
	SecurityOpt          []string               `json:"securityOpt,omitempty"`
	Entrypoint           string                 `json:"entrypoint,omitempty"`
	OnCreateCommand      interface{}            `json:"onCreateCommand,omitempty"`
	UpdateContentCommand interface{}            `json:"updateContentCommand,omitempty"`
	PostCreateCommand    interface{}            `json:"postCreateCommand,omitempty"`
	PostStartCommand     interface{}            `json:"postStartCommand,omitempty"`
	PostAttachCommand    interface{}            `json:"postAttachCommand,omitempty"`
	Customizations       map[string]interface{} `json:"customizations,omitempty"`
}

// SourceInfo describes where a feature comes from.
type SourceInfo interface {
	SourceType() string
	UserFeatureID() string
}

// OCISource is a feature from an OCI registry.
type OCISource struct {
	Type                        string      `json:"type"`
	ManifestDigest              string      `json:"manifestDigest,omitempty"`
	UserID                      string      `json:"userFeatureId"`
	UserFeatureIdWithoutVersion string      `json:"userFeatureIdWithoutVersion,omitempty"`
	FeatureRef                  interface{} `json:"featureRef,omitempty"`
	Manifest                    interface{} `json:"manifest,omitempty"`
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

// LocalSource is a feature from a local file path.
type LocalSource struct {
	LocalPath    string
	ResolvedPath string
	UserID       string
}

func (s *LocalSource) SourceType() string    { return "file-path" }
func (s *LocalSource) UserFeatureID() string { return s.UserID }

// Config holds the fully resolved features configuration.
type Config struct {
	FeatureSets []*Set
}

// DevContainerFeature is a user's feature reference from devcontainer.json.
type DevContainerFeature struct {
	UserFeatureID string
	Options       interface{} // bool | string | map[string]interface{}
}

// UserFeaturesToArray converts the features map from devcontainer.json
// into a slice of DevContainerFeature.
func UserFeaturesToArray(features map[string]interface{}) []DevContainerFeature {
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
