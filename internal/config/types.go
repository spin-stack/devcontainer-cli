package config

import (
	"encoding/json"
)

// DevContainerConfig represents a devcontainer.json.
// It is a flat struct covering all three variants (image, Dockerfile, docker-compose).
// Use IsDockerfileConfig / IsComposeConfig to discriminate.
type DevContainerConfig struct {
	// Runtime — not serialized from JSON
	ConfigFilePath string `json:"configFilePath,omitempty"`

	// --- Common properties (all variants) ---
	Name                        string                 `json:"name,omitempty"`
	ForwardPorts                []PortOrString         `json:"forwardPorts,omitempty"`
	AppPort                     json.RawMessage        `json:"appPort,omitempty"` // number | string | (number|string)[]
	PortsAttributes             map[string]PortAttrs   `json:"portsAttributes,omitempty"`
	OtherPortsAttributes        *PortAttrs             `json:"otherPortsAttributes,omitempty"`
	RunArgs                     []string               `json:"runArgs,omitempty"`
	ShutdownAction              string                 `json:"shutdownAction,omitempty"`
	OverrideCommand             *bool                  `json:"overrideCommand,omitempty"`
	InitializeCommand           LifecycleCommand       `json:"initializeCommand,omitempty"`
	OnCreateCommand             LifecycleCommand       `json:"onCreateCommand,omitempty"`
	UpdateContentCommand        LifecycleCommand       `json:"updateContentCommand,omitempty"`
	PostCreateCommand           LifecycleCommand       `json:"postCreateCommand,omitempty"`
	PostStartCommand            LifecycleCommand       `json:"postStartCommand,omitempty"`
	PostAttachCommand           LifecycleCommand       `json:"postAttachCommand,omitempty"`
	WaitFor                     string                 `json:"waitFor,omitempty"`
	WorkspaceFolder             string                 `json:"workspaceFolder,omitempty"`
	WorkspaceMount              string                 `json:"workspaceMount,omitempty"`
	Mounts                      []MountOrString        `json:"mounts,omitempty"`
	ContainerEnv                map[string]string      `json:"containerEnv,omitempty"`
	ContainerUser               string                 `json:"containerUser,omitempty"`
	Init                        *bool                  `json:"init,omitempty"`
	Privileged                  *bool                  `json:"privileged,omitempty"`
	CapAdd                      []string               `json:"capAdd,omitempty"`
	SecurityOpt                 []string               `json:"securityOpt,omitempty"`
	RemoteEnv                   map[string]*string     `json:"remoteEnv,omitempty"`
	RemoteUser                  string                 `json:"remoteUser,omitempty"`
	UpdateRemoteUserUID         *bool                  `json:"updateRemoteUserUID,omitempty"`
	UserEnvProbe                string                 `json:"userEnvProbe,omitempty"`
	Features                    map[string]interface{} `json:"features,omitempty"`
	OverrideFeatureInstallOrder []string               `json:"overrideFeatureInstallOrder,omitempty"`
	HostRequirements            *HostRequirements      `json:"hostRequirements,omitempty"`
	Customizations              map[string]interface{} `json:"customizations,omitempty"`

	// --- Image variant ---
	Image string `json:"image,omitempty"`

	// --- Dockerfile variant ---
	DockerFile string       `json:"dockerFile,omitempty"` // legacy top-level
	Build      *BuildConfig `json:"build,omitempty"`
	Context    string       `json:"context,omitempty"` // legacy top-level

	// --- Docker Compose variant ---
	DockerComposeFile StringOrStrings `json:"dockerComposeFile,omitempty"`
	Service           string          `json:"service,omitempty"`
	RunServices       []string        `json:"runServices,omitempty"`

	// --- Deprecated / VS Code specific (migrated by UpdateFromOldProperties) ---
	Extensions []string    `json:"extensions,omitempty"`
	Settings   interface{} `json:"settings,omitempty"`
	DevPort    *int        `json:"devPort,omitempty"`
}

// BuildConfig represents the "build" property in devcontainer.json.
type BuildConfig struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	CacheFrom  StringOrStrings   `json:"cacheFrom,omitempty"`
	Options    []string          `json:"options,omitempty"`
}

// PortAttrs represents port attribute configuration.
type PortAttrs struct {
	Label           string `json:"label,omitempty"`
	OnAutoForward   string `json:"onAutoForward,omitempty"`
	ElevateIfNeeded *bool  `json:"elevateIfNeeded,omitempty"`
}

// HostRequirements specifies minimum host resources.
type HostRequirements struct {
	CPUs    *int            `json:"cpus,omitempty"`
	Memory  string          `json:"memory,omitempty"`
	Storage string          `json:"storage,omitempty"`
	GPU     json.RawMessage `json:"gpu,omitempty"` // bool | "optional" | object
}

// Mount represents a structured mount definition.
type Mount struct {
	Type     string `json:"type,omitempty"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	External *bool  `json:"external,omitempty"`
}

// --- Type discrimination ---

// IsDockerfileConfig returns true if the config specifies a Dockerfile build.
func (c *DevContainerConfig) IsDockerfileConfig() bool {
	return c.DockerFile != "" || (c.Build != nil && c.Build.Dockerfile != "")
}

// IsComposeConfig returns true if the config specifies docker-compose.
func (c *DevContainerConfig) IsComposeConfig() bool {
	return len(c.DockerComposeFile) > 0
}

// IsImageConfig returns true if the config specifies a pre-built image (no Dockerfile or Compose).
func (c *DevContainerConfig) IsImageConfig() bool {
	return !c.IsDockerfileConfig() && !c.IsComposeConfig()
}

// GetDockerfile returns the Dockerfile path from either legacy or build.dockerfile.
func (c *DevContainerConfig) GetDockerfile() string {
	if c.DockerFile != "" {
		return c.DockerFile
	}
	if c.Build != nil {
		return c.Build.Dockerfile
	}
	return ""
}

// GetBuildContext returns the build context path.
func (c *DevContainerConfig) GetBuildContext() string {
	if c.Context != "" {
		return c.Context
	}
	if c.Build != nil && c.Build.Context != "" {
		return c.Build.Context
	}
	return ""
}

// --- Custom union types ---

// LifecycleCommand can be string, []string, or map[string]interface{}.
// This matches the TS type: string | string[] | Record<string, string | string[]>.
type LifecycleCommand struct {
	raw interface{} // string, []interface{}, or map[string]interface{}
}

func (c *LifecycleCommand) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &c.raw)
}

func (c LifecycleCommand) MarshalJSON() ([]byte, error) {
	if c.raw == nil {
		return []byte("null"), nil
	}
	return json.Marshal(c.raw)
}

// IsEmpty returns true if no command is specified.
func (c *LifecycleCommand) IsEmpty() bool {
	return c.raw == nil
}

// AsString returns the command as a string, or empty if not a string.
func (c *LifecycleCommand) AsString() (string, bool) {
	s, ok := c.raw.(string)
	return s, ok
}

// AsStringSlice returns the command as []string, or nil.
func (c *LifecycleCommand) AsStringSlice() ([]string, bool) {
	arr, ok := c.raw.([]interface{})
	if !ok {
		return nil, false
	}
	strs := make([]string, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		strs[i] = s
	}
	return strs, true
}

// AsMap returns the command as a map (for parallel commands), or nil.
func (c *LifecycleCommand) AsMap() (map[string]interface{}, bool) {
	m, ok := c.raw.(map[string]interface{})
	return m, ok
}

// Raw returns the underlying value.
func (c *LifecycleCommand) Raw() interface{} {
	return c.raw
}

// PortOrString represents a port that can be number or string (e.g., "8080:80").
type PortOrString struct {
	raw interface{} // float64 (JSON number) or string
}

func (p *PortOrString) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &p.raw)
}

func (p PortOrString) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.raw)
}

// MountOrString represents either a Mount object or a string mount spec.
type MountOrString struct {
	raw interface{} // string or map → Mount
}

func (m *MountOrString) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.raw = s
		return nil
	}
	// Try mount object
	var mount Mount
	if err := json.Unmarshal(data, &mount); err == nil {
		m.raw = mount
		return nil
	}
	return json.Unmarshal(data, &m.raw)
}

func (m MountOrString) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.raw)
}

// AsString returns the mount as a string, or empty.
func (m *MountOrString) AsString() (string, bool) {
	s, ok := m.raw.(string)
	return s, ok
}

// AsMount returns the mount as a structured Mount.
func (m *MountOrString) AsMount() (*Mount, bool) {
	mt, ok := m.raw.(Mount)
	if ok {
		return &mt, true
	}
	return nil, false
}

// StringOrStrings can be a single string or an array of strings.
// Used for dockerComposeFile, cacheFrom.
type StringOrStrings []string

func (s *StringOrStrings) UnmarshalJSON(data []byte) error {
	// Try single string
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	// Try array
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

func (s StringOrStrings) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

// --- Legacy property migration ---

// UpdateFromOldProperties migrates deprecated extensions/settings/devPort
// to customizations.vscode.*, matching the TS function in configuration.ts.
func UpdateFromOldProperties(c *DevContainerConfig) {
	if len(c.Extensions) == 0 && c.Settings == nil && c.DevPort == nil {
		return
	}

	if c.Customizations == nil {
		c.Customizations = make(map[string]interface{})
	}
	vscodeRaw, _ := c.Customizations["vscode"].(map[string]interface{})
	if vscodeRaw == nil {
		vscodeRaw = make(map[string]interface{})
	}

	if len(c.Extensions) > 0 {
		existing, _ := vscodeRaw["extensions"].([]interface{})
		for _, ext := range c.Extensions {
			existing = append(existing, ext)
		}
		vscodeRaw["extensions"] = existing
		c.Extensions = nil
	}

	if c.Settings != nil {
		if existingSettings, ok := vscodeRaw["settings"].(map[string]interface{}); ok {
			if newSettings, ok := c.Settings.(map[string]interface{}); ok {
				for k, v := range newSettings {
					if _, exists := existingSettings[k]; !exists {
						existingSettings[k] = v
					}
				}
			}
		} else {
			vscodeRaw["settings"] = c.Settings
		}
		c.Settings = nil
	}

	if c.DevPort != nil {
		if _, exists := vscodeRaw["devPort"]; !exists {
			vscodeRaw["devPort"] = *c.DevPort
		}
		c.DevPort = nil
	}

	c.Customizations["vscode"] = vscodeRaw
}
