package templates

type TemplateMetadata struct {
	ID          string         `json:"id"`
	Version     string         `json:"version,omitempty"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	Platforms   []string       `json:"platforms,omitempty"`
}

type SelectedTemplate struct {
	ID        string
	Options   map[string]string
	Features  []TemplateFeatureOption
	OmitPaths []string
}

type TemplateFeatureOption struct {
	ID      string         `json:"id"`
	Options map[string]any `json:"options,omitempty"`
}

type CollectionMetadata struct {
	Templates []TemplateMetadata `json:"templates,omitempty"`
	Features  []any              `json:"features,omitempty"`
}
