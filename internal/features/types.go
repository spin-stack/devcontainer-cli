// Package features resolves, orders, installs and locks dev container Features.
package features

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/jsonc"
)

// Set represents a resolved feature with its source and metadata.
type Set struct {
	SourceInfo SourceInfo `json:"sourceInformation"`
	Features   []Feature  `json:"features"`
	// TS applyFeatureConfigToFeature sets internalVersion, then computedDigest.
	InternalVersion string `json:"internalVersion,omitempty"`
	ComputedDigest  string `json:"computedDigest,omitempty"`
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
	// Metadata is the devcontainer-feature.json UnmarshalJSON decoded the
	// Feature from, as standard JSON; File, when set, is the Feature's
	// devcontainer-feature.json where it differs from Metadata (an OCI
	// Feature's metadata comes from its manifest's annotation, the file from
	// its layer).
	Metadata json.RawMessage `json:"-"`
	File     json.RawMessage `json:"-"`
	// SourceKeys, when set, are the properties TS processFeatureIdentifier
	// gives the Feature before anything else (LocalFeatureKeys or
	// OCIFeatureKeys), and the Feature is written as TS builds it (see
	// MarshalJSON).
	SourceKeys []string `json:"-"`
}

// The properties TS processFeatureIdentifier starts a Feature with: a local
// or tarball Feature's {id, name, value, included}, an OCI one's
// {id, included, value} (tryGetOCIFeatureSet).
var (
	LocalFeatureKeys = []string{"id", "name", "value", "included"}
	OCIFeatureKeys   = []string{"id", "included", "value"}
)

// MarshalJSON writes a Feature with SourceKeys as TS builds it for
// featuresConfiguration, and one without in the struct's order. TS starts
// from the SourceKeys properties, spreads the metadata over them when it
// reads the dependencies (buildDependencyGraph), adds cachePath and
// consecutiveId (fetchFeatures), spreads the result over the
// devcontainer-feature.json (applyFeatureConfigToFeature) and moves the
// deprecated extensions and settings into customizations
// (updateFromOldProperties). The
// metadata's own properties, values and key order are kept, those Go has no
// field for included.
func (f Feature) MarshalJSON() ([]byte, error) {
	type plain Feature
	if f.SourceKeys == nil {
		return json.Marshal(plain(f))
	}
	feature := newJSObject()
	for _, k := range f.SourceKeys {
		var v any
		switch k {
		case "id":
			v = f.ID
		case "name":
			v = f.Name
		case "value":
			v = f.Value
		case "included":
			v = f.Included
		}
		js, err := toJS(v)
		if err != nil {
			return nil, err
		}
		feature.set(k, js)
	}
	meta, err := metadataObject(f.Metadata)
	if err != nil {
		return nil, err
	}
	feature = feature.spread(meta)
	if f.CachePath != "" {
		feature.set("cachePath", f.CachePath)
	}
	if f.ConsecutiveId != "" {
		feature.set("consecutiveId", f.ConsecutiveId)
	}
	file := meta
	if f.File != nil {
		if file, err = metadataObject(f.File); err != nil {
			return nil, err
		}
	}
	out, err := updateFeatureFromOldProperties(file.spread(feature))
	if err != nil {
		return nil, err
	}
	return out.MarshalJSON()
}

// metadataObject is the metadata's object, empty when there is none (TS
// spreads only a metadata it read).
func metadataObject(data json.RawMessage) (*jsObject, error) {
	if len(data) == 0 {
		return newJSObject(), nil
	}
	v, err := parseJS(data)
	if err != nil {
		return nil, err
	}
	if obj, ok := v.(*jsObject); ok {
		return obj, nil
	}
	return spreadSource(v), nil
}

// updateFeatureFromOldProperties is TS updateFromOldProperties
// (containerFeaturesConfiguration.ts) on a Feature: its extensions and
// settings move into customizations.vscode.
func updateFeatureFromOldProperties(original *jsObject) (*jsObject, error) {
	extensions, _ := original.get("extensions")
	settings, _ := original.get("settings")
	if !jsTruthy(extensions) && !jsTruthy(settings) {
		return original, nil
	}
	cp := original.spread(newJSObject())
	customizations := orNewObject(cp, "customizations")
	vscode := orNewObject(customizations, "vscode")
	if jsTruthy(extensions) {
		base, _ := vscode.get("extensions")
		joined, err := jsConcat(base, extensions)
		if err != nil {
			return nil, err
		}
		vscode.set("extensions", joined)
		cp.remove("extensions")
	}
	if jsTruthy(settings) {
		vsSettings, _ := vscode.get("settings")
		vscode.set("settings", spreadSource(settings).spread(spreadSource(vsSettings)))
		cp.remove("settings")
	}
	return cp, nil
}

// orNewObject is obj[k] || (obj[k] = {}), for an object or a new one.
func orNewObject(obj *jsObject, k string) *jsObject {
	if v, _ := obj.get(k); jsTruthy(v) {
		if o, ok := v.(*jsObject); ok {
			return o
		}
	}
	o := newJSObject()
	obj.set(k, o)
	return o
}

// jsConcat is (base || []).concat(v): an array's elements are appended, any
// other value is; a string base concatenates as a string does.
func jsConcat(base, v any) (any, error) {
	if !jsTruthy(base) {
		base = []any{}
	}
	switch b := base.(type) {
	case []any:
		out := append([]any{}, b...)
		if arr, ok := v.([]any); ok {
			return append(out, arr...), nil
		}
		return append(out, v), nil
	case string:
		s, err := jsString(v)
		return b + s, err
	}
	return nil, fmt.Errorf("TypeError: (vscode.extensions || []).concat is not a function")
}

// jsString is String(v) for the values a string concat appends.
func jsString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool, float64:
		data, err := json.Marshal(x)
		return string(data), err
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil {
				continue
			}
			s, err := jsString(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	}
	return "[object Object]", nil
}

// UnmarshalJSON decodes the Feature and records the order of its dependsOn
// keys in DependsOnOrder.
func (f *Feature) UnmarshalJSON(data []byte) error {
	type plain Feature
	if err := json.Unmarshal(data, (*plain)(f)); err != nil {
		return err
	}
	f.DependsOnOrder = jsonc.MemberNames(data, "dependsOn")
	f.Metadata = slices.Clone(data)
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
// Its properties are in the order of TS OCISourceInformation as
// tryGetOCIFeatureSet builds it.
type OCISource struct {
	Type string `json:"type"`
	// Manifest is the manifest as the registry served it (TS manifestObj, its
	// JSON.parse): see ManifestJSON.
	Manifest                    any         `json:"manifest,omitempty"`
	ManifestDigest              string      `json:"manifestDigest,omitempty"`
	FeatureRef                  *FeatureRef `json:"featureRef,omitempty"`
	UserID                      string      `json:"userFeatureId"`
	UserFeatureIdWithoutVersion string      `json:"userFeatureIdWithoutVersion,omitempty"`
	// Internal fields not serialized to JSON (used for dependency resolution)
	Registry  string `json:"-"`
	Namespace string `json:"-"`
	ID        string `json:"-"`
	Resource  string `json:"-"`
	Tag       string `json:"-"`
}

// FeatureRef is TS OCIRef as getRef builds it, its properties in that order.
type FeatureRef struct {
	Scheme    string `json:"scheme"`
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Namespace string `json:"namespace"`
	Registry  string `json:"registry"`
	Resource  string `json:"resource"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Tag       string `json:"tag,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

// ManifestJSON is a manifest as TS keeps it in OCISourceInformation:
// JSON.parse of the body the registry served, properties in its order. A
// body that does not parse is written as fallback is.
func ManifestJSON(body []byte, fallback any) any {
	if v, err := parseJS(body); err == nil {
		return v
	}
	return fallback
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
