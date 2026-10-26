package config

import (
	"encoding/json"
	"maps"
)

// DevContainer represents a devcontainer.json.
// It is a flat struct covering all three variants (image, Dockerfile, docker-compose).
// Use IsDockerfileConfig / IsComposeConfig to discriminate.
type DevContainer struct {
	// Runtime — not serialized from JSON
	ConfigFilePath string `json:"configFilePath,omitempty"`
	// workspaceMountSet records that the configuration has a workspaceMount
	// property, even an empty or null one, which WorkspaceMount cannot tell
	// from an absent one (TS getWorkspaceConfiguration's `'workspaceMount' in
	// config`).
	workspaceMountSet bool

	// --- Common properties (all variants) ---
	Name                        string               `json:"name,omitempty"`
	ForwardPorts                []PortOrString       `json:"forwardPorts,omitempty"`
	AppPort                     json.RawMessage      `json:"appPort,omitempty"` // number | string | (number|string)[]
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
	// Secrets is the spec's "recommended secrets" metadata (env-var key ->
	// {description, documentationUrl}). The CLI does not act on it; kept as an
	// open map so it round-trips losslessly through read-configuration/metadata.
	Secrets map[string]any `json:"secrets,omitempty"`

	// --- Image variant ---
	Image string `json:"image,omitempty"`

	// --- Dockerfile variant ---
	DockerFile string `json:"dockerFile,omitempty"` // legacy top-level
	Build      *Build `json:"build,omitempty"`
	Context    string `json:"context,omitempty"` // legacy top-level

	// --- Docker Compose variant ---
	DockerComposeFile StringOrStrings `json:"dockerComposeFile,omitempty"`
	Service           string          `json:"service,omitempty"`
	RunServices       []string        `json:"runServices,omitempty"`

	// --- Deprecated / VS Code specific (migrated by UpdateFromOldProperties) ---
	// Untyped: TS neither validates nor reads what updateFromOldProperties
	// leaves behind (a falsy "extensions", a "devPort" vscode already has).
	Extensions any `json:"extensions,omitempty"`
	Settings   any `json:"settings,omitempty"`
	DevPort    any `json:"devPort,omitempty"`
}

// Customizations is the "customizations" object of a configuration. TS
// neither validates it nor fails on another JSON value; such a value is no
// customizations here, and the raw configuration still carries it as written.
type Customizations map[string]any

// UnmarshalJSON decodes an object; any other JSON value decodes to nil.
func (c *Customizations) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m, _ := v.(map[string]any)
	*c = m
	return nil
}

// Build represents the "build" property in devcontainer.json.
type Build struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	CacheFrom  StringOrStrings   `json:"cacheFrom,omitempty"`
	Options    []string          `json:"options,omitempty"`
}

// PortAttrs represents port attribute configuration. Fields mirror the spec's
// PortAttributes object (containers.dev json_reference): label, protocol
// (http|https), onAutoForward, requireLocalPort, elevateIfNeeded. The CLI does
// not act on these (port forwarding is the editor's job) but must preserve them
// through read-configuration and the devcontainer.metadata label.
type PortAttrs struct {
	Label            string `json:"label,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	OnAutoForward    string `json:"onAutoForward,omitempty"`
	RequireLocalPort *bool  `json:"requireLocalPort,omitempty"`
	ElevateIfNeeded  *bool  `json:"elevateIfNeeded,omitempty"`
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
	// Readonly maps a mount object to a read-only bind/volume for BOTH the
	// docker (`--mount ...,readonly`) and docker-compose (`:ro`) paths, so a
	// single Feature/config mount object works in either mode (upstream #881).
	Readonly *bool `json:"readonly,omitempty"`
}

// --- Type discrimination ---

// IsDockerfileConfig returns true if the config specifies a Dockerfile build.
func (c *DevContainer) IsDockerfileConfig() bool {
	return c.DockerFile != "" || (c.Build != nil && c.Build.Dockerfile != "")
}

// IsComposeConfig returns true if the config specifies docker-compose.
func (c *DevContainer) IsComposeConfig() bool {
	return len(c.DockerComposeFile) > 0
}

// IsImageConfig returns true if the config specifies a pre-built image (no Dockerfile or Compose).
func (c *DevContainer) IsImageConfig() bool {
	return !c.IsDockerfileConfig() && !c.IsComposeConfig()
}

// Dockerfile returns the Dockerfile path from either legacy or build.dockerfile.
func (c *DevContainer) Dockerfile() string {
	if c.DockerFile != "" {
		return c.DockerFile
	}
	if c.Build != nil {
		return c.Build.Dockerfile
	}
	return ""
}

// BuildContext returns the build context path.
func (c *DevContainer) BuildContext() string {
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
	raw any // string, []interface{}, or map[string]interface{}
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

// AsMap returns the command as a map (for parallel commands), or nil.
func (c *LifecycleCommand) AsMap() (map[string]any, bool) {
	m, ok := c.raw.(map[string]any)
	return m, ok
}

// Raw returns the underlying value.
func (c *LifecycleCommand) Raw() any {
	return c.raw
}

// PortOrString represents a port that can be number or string (e.g., "8080:80").
type PortOrString struct {
	raw any // float64 (JSON number) or string
}

func (p *PortOrString) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &p.raw)
}

func (p PortOrString) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.raw)
}

// Value returns the port as decoded: a float64 for a JSON number, else a string.
func (p PortOrString) Value() any {
	return p.raw
}

// MountOrString represents either a Mount object or a string mount spec.
type MountOrString struct {
	raw any // string or map → Mount
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

// updateFromOldProperties is TS updateFromOldProperties: it moves the
// deprecated top-level extensions, settings and devPort of a parsed
// devcontainer.json into customizations.vscode, on a copy. extensions are
// appended to customizations.vscode.extensions; settings are merged under
// customizations.vscode.settings, whose keys win; devPort moves only when
// customizations.vscode has none, and otherwise stays where it is; a null
// devPort is present, as for the TS `devPort !== undefined`.
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

// truthy is JavaScript truthiness for a JSON value.
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
