package features

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/jsonc"
)

type Set struct {
	SourceInfo SourceInfo `json:"sourceInformation"`
	Features   []Feature  `json:"features"`
	// TS applyFeatureConfigToFeature sets internalVersion, then computedDigest.
	InternalVersion string `json:"internalVersion,omitempty"`
	ComputedDigest  string `json:"computedDigest,omitempty"`
}

type Feature struct {
	ID                   string            `json:"id"`
	Version              string            `json:"version,omitempty"`
	Name                 string            `json:"name,omitempty"`
	Description          string            `json:"description,omitempty"`
	DocumentationURL     string            `json:"documentationURL,omitempty"`
	Options              map[string]any    `json:"options,omitempty"`
	DependsOn            map[string]any    `json:"dependsOn,omitempty"`
	DependsOnOrder       []string          `json:"-"`
	InstallsAfter        []string          `json:"installsAfter,omitempty"`
	LegacyIds            []string          `json:"legacyIds,omitempty"`
	Included             bool              `json:"included"`
	CachePath            string            `json:"cachePath,omitempty"`
	ConsecutiveId        string            `json:"consecutiveId,omitempty"`
	Value                any               `json:"value"`
	UserOptions          map[string]any    `json:"-"`
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
	Metadata             json.RawMessage   `json:"-"`
	File                 json.RawMessage   `json:"-"`
	SourceKeys           []string          `json:"-"`
}

var (
	LocalFeatureKeys = []string{"id", "name", "value", "included"}
	OCIFeatureKeys   = []string{"id", "included", "value"}
)

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

func (f *Feature) UnmarshalJSON(data []byte) error {
	type plain Feature
	if err := json.Unmarshal(data, (*plain)(f)); err != nil {
		return err
	}
	f.DependsOnOrder = jsonc.MemberNames(data, "dependsOn")
	f.Metadata = slices.Clone(data)
	return nil
}

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

type SourceInfo interface {
	SourceType() string
	UserFeatureID() string
}

// Its properties are in the order of TS OCISourceInformation as
// tryGetOCIFeatureSet builds it.
type OCISource struct {
	Type                        string      `json:"type"`
	Manifest                    any         `json:"manifest,omitempty"`
	ManifestDigest              string      `json:"manifestDigest,omitempty"`
	FeatureRef                  *FeatureRef `json:"featureRef,omitempty"`
	UserID                      string      `json:"userFeatureId"`
	UserFeatureIdWithoutVersion string      `json:"userFeatureIdWithoutVersion,omitempty"`
	Registry                    string      `json:"-"`
	Namespace                   string      `json:"-"`
	ID                          string      `json:"-"`
	Resource                    string      `json:"-"`
	Tag                         string      `json:"-"`
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

func ManifestJSON(body []byte, fallback any) any {
	if v, err := parseJS(body); err == nil {
		return v
	}
	return fallback
}

func (s *OCISource) SourceType() string    { return "oci" }
func (s *OCISource) UserFeatureID() string { return s.UserID }

type TarballSource struct {
	TarballURI string
	UserID     string
}

func (s *TarballSource) SourceType() string    { return "direct-tarball" }
func (s *TarballSource) UserFeatureID() string { return s.UserID }

func (s *TarballSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type          string `json:"type"`
		TarballURI    string `json:"tarballUri"`
		UserFeatureID string `json:"userFeatureId"`
	}{s.SourceType(), s.TarballURI, s.UserID})
}

type LocalSource struct {
	LocalPath    string
	ResolvedPath string
	UserID       string
}

func (s *LocalSource) SourceType() string    { return "file-path" }
func (s *LocalSource) UserFeatureID() string { return s.UserID }

func (s *LocalSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		ResolvedFilePath string `json:"resolvedFilePath"`
		UserFeatureID    string `json:"userFeatureId"`
	}{s.SourceType(), s.ResolvedPath, s.UserID})
}

type Config struct {
	FeatureSets []*Set
}

type DevContainerFeature struct {
	UserFeatureID string
	Options       any
}

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
