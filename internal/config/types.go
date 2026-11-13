package config

import (
	"encoding/json"
	"maps"
)

type DevContainer struct {
	ConfigFilePath    string `json:"configFilePath,omitempty"`
	workspaceMountSet bool

	Name                        string               `json:"name,omitempty"`
	ForwardPorts                []PortOrString       `json:"forwardPorts,omitempty"`
	AppPort                     json.RawMessage      `json:"appPort,omitempty"`
	PortsAttributes             map[string]PortAttrs `json:"portsAttributes,omitempty"`
	OtherPortsAttributes        *PortAttrs           `json:"otherPortsAttributes,omitempty"`
	RunArgs                     []string             `json:"runArgs,omitempty"`
	ShutdownAction              string               `json:"shutdownAction,omitempty"`
	OverrideCommand             *bool                `json:"overrideCommand,omitempty"`
	InitializeCommand           LifecycleCommand     `json:"initializeCommand"`
	OnCreateCommand             LifecycleCommand     `json:"onCreateCommand"`
	UpdateContentCommand        LifecycleCommand     `json:"updateContentCommand"`
	PostCreateCommand           LifecycleCommand     `json:"postCreateCommand"`
	PostStartCommand            LifecycleCommand     `json:"postStartCommand"`
	PostAttachCommand           LifecycleCommand     `json:"postAttachCommand"`
	WaitFor                     string               `json:"waitFor,omitempty"`
	WorkspaceFolder             string               `json:"workspaceFolder,omitempty"`
	WorkspaceMount              string               `json:"workspaceMount,omitempty"`
	Mounts                      []MountOrString      `json:"mounts,omitempty"`
	ContainerEnv                map[string]string    `json:"containerEnv,omitempty"`
	ContainerUser               string               `json:"containerUser,omitempty"`
	Init                        *bool                `json:"init,omitempty"`
	Privileged                  *bool                `json:"privileged,omitempty"`
	CapAdd                      []string             `json:"capAdd,omitempty"`
	SecurityOpt                 []string             `json:"securityOpt,omitempty"`
	RemoteEnv                   map[string]*string   `json:"remoteEnv,omitempty"`
	RemoteUser                  string               `json:"remoteUser,omitempty"`
	UpdateRemoteUserUID         *bool                `json:"updateRemoteUserUID,omitempty"`
	UserEnvProbe                string               `json:"userEnvProbe,omitempty"`
	Features                    map[string]any       `json:"features,omitempty"`
	OverrideFeatureInstallOrder []string             `json:"overrideFeatureInstallOrder,omitempty"`
	HostRequirements            *HostRequirements    `json:"hostRequirements,omitempty"`
	Customizations              Customizations       `json:"customizations,omitempty"`
	Secrets                     map[string]any       `json:"secrets,omitempty"`

	Image string `json:"image,omitempty"`

	DockerFile string `json:"dockerFile,omitempty"`
	Build      *Build `json:"build,omitempty"`
	Context    string `json:"context,omitempty"`

	DockerComposeFile StringOrStrings `json:"dockerComposeFile,omitempty"`
	Service           string          `json:"service,omitempty"`
	RunServices       []string        `json:"runServices,omitempty"`

	Extensions any `json:"extensions,omitempty"`
	Settings   any `json:"settings,omitempty"`
	DevPort    any `json:"devPort,omitempty"`

	ContainerEnvOrder  []string            `json:"-"`
	FeatureOptionOrder map[string][]string `json:"-"`
}

type Customizations map[string]any

func (c *Customizations) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m, _ := v.(map[string]any)
	*c = m
	return nil
}

type Build struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	CacheFrom  StringOrStrings   `json:"cacheFrom,omitempty"`
	Options    []string          `json:"options,omitempty"`
}

type PortAttrs struct {
	Label            string `json:"label,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	OnAutoForward    string `json:"onAutoForward,omitempty"`
	RequireLocalPort *bool  `json:"requireLocalPort,omitempty"`
	ElevateIfNeeded  *bool  `json:"elevateIfNeeded,omitempty"`
}

type HostRequirements struct {
	CPUs    *int            `json:"cpus,omitempty"`
	Memory  string          `json:"memory,omitempty"`
	Storage string          `json:"storage,omitempty"`
	GPU     json.RawMessage `json:"gpu,omitempty"`
}

type Mount struct {
	Type     string `json:"type,omitempty"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	External *bool  `json:"external,omitempty"`
	Readonly *bool  `json:"readonly,omitempty"`
}

func (c *DevContainer) IsDockerfileConfig() bool {
	return c.DockerFile != "" || (c.Build != nil && c.Build.Dockerfile != "")
}

func (c *DevContainer) IsComposeConfig() bool {
	return len(c.DockerComposeFile) > 0
}

func (c *DevContainer) IsImageConfig() bool {
	return !c.IsDockerfileConfig() && !c.IsComposeConfig()
}

func (c *DevContainer) Dockerfile() string {
	if c.DockerFile != "" {
		return c.DockerFile
	}
	if c.Build != nil {
		return c.Build.Dockerfile
	}
	return ""
}

func (c *DevContainer) BuildContext() string {
	if c.Context != "" {
		return c.Context
	}
	if c.Build != nil && c.Build.Context != "" {
		return c.Build.Context
	}
	return ""
}

type LifecycleCommand struct {
	raw any
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

func (c *LifecycleCommand) IsEmpty() bool {
	return c.raw == nil
}

func (c *LifecycleCommand) AsString() (string, bool) {
	s, ok := c.raw.(string)
	return s, ok
}

func (c *LifecycleCommand) AsStringSlice() ([]string, bool) {
	arr, ok := c.raw.([]any)
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

func (c *LifecycleCommand) AsMap() (map[string]any, bool) {
	m, ok := c.raw.(map[string]any)
	return m, ok
}

func (c *LifecycleCommand) Raw() any {
	return c.raw
}

type PortOrString struct {
	raw any
}

func (p *PortOrString) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &p.raw)
}

func (p PortOrString) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.raw)
}

func (p PortOrString) Value() any {
	return p.raw
}

type MountOrString struct {
	raw any
}

func (m *MountOrString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.raw = s
		return nil
	}
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

func (m *MountOrString) AsString() (string, bool) {
	s, ok := m.raw.(string)
	return s, ok
}

func (m *MountOrString) AsMount() (*Mount, bool) {
	mt, ok := m.raw.(Mount)
	if ok {
		return &mt, true
	}
	return nil, false
}

type StringOrStrings []string

func (s *StringOrStrings) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
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

func updateFromOldProperties(raw map[string]any) map[string]any {
	devPort, hasDevPort := raw["devPort"]
	if !truthy(raw["extensions"]) && !truthy(raw["settings"]) && !hasDevPort {
		return raw
	}
	out := maps.Clone(raw)
	customizations, _ := out["customizations"].(map[string]any)
	customizations = maps.Clone(customizations)
	if customizations == nil {
		customizations = map[string]any{}
	}
	vscode, _ := customizations["vscode"].(map[string]any)
	vscode = maps.Clone(vscode)
	if vscode == nil {
		vscode = map[string]any{}
	}
	if truthy(out["extensions"]) {
		existing, _ := vscode["extensions"].([]any)
		added, ok := out["extensions"].([]any)
		if !ok {
			added = []any{out["extensions"]}
		}
		vscode["extensions"] = append(append([]any{}, existing...), added...)
		delete(out, "extensions")
	}
	if truthy(out["settings"]) {
		merged := map[string]any{}
		if m, ok := out["settings"].(map[string]any); ok {
			maps.Copy(merged, m)
		}
		if m, ok := vscode["settings"].(map[string]any); ok {
			maps.Copy(merged, m)
		}
		vscode["settings"] = merged
		delete(out, "settings")
	}
	if hasDevPort {
		if _, set := vscode["devPort"]; !set {
			vscode["devPort"] = devPort
			delete(out, "devPort")
		}
	}
	customizations["vscode"] = vscode
	out["customizations"] = customizations
	return out
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return true
	}
}
