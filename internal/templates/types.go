package templates

// TemplateMetadata represents a devcontainer-template.json.
type TemplateMetadata struct {
	ID          string                 `json:"id"`
	Version     string                 `json:"version,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Options     map[string]interface{} `json:"options,omitempty"`
	Platforms   []string               `json:"platforms,omitempty"`
}

// SelectedTemplate holds user selections for applying a template.
type SelectedTemplate struct {
	ID        string
	Options   map[string]string
	Features  []TemplateFeatureOption
	OmitPaths []string
}

// TemplateFeatureOption is a feature to add when applying a template.
type TemplateFeatureOption struct {
	ID      string                 `json:"id"`
	Options map[string]interface{} `json:"options,omitempty"`
}

// CollectionMetadata is the metadata for a collection of templates/features.
type CollectionMetadata struct {
	Templates []TemplateMetadata `json:"templates,omitempty"`
	Features  []interface{}      `json:"features,omitempty"`
}
