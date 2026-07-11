package docker

import (
	"encoding/json"
	"os"
	"strings"
)

// ComposeOverride represents a Docker Compose override file.
// Serialized as JSON, which Docker Compose v2 reads natively.
type ComposeOverride struct {
	Services map[string]*ServiceOverride `json:"services"`
	Volumes  map[string]interface{}      `json:"volumes,omitempty"`
}

// ServiceOverride represents overrides for a single compose service.
type ServiceOverride struct {
	Image       string         `json:"image,omitempty"`
	Build       *BuildOverride `json:"build,omitempty"`
	Entrypoint  []string       `json:"entrypoint,omitempty"`
	Command     []string       `json:"command,omitempty"`
	Environment []string       `json:"environment,omitempty"`
	User        string         `json:"user,omitempty"`
	Privileged  *bool          `json:"privileged,omitempty"`
	Init        *bool          `json:"init,omitempty"`
	CapAdd      []string       `json:"cap_add,omitempty"`
	SecurityOpt []string       `json:"security_opt,omitempty"`
	VolumesSpec []string       `json:"volumes,omitempty"`
	Labels      []string       `json:"labels,omitempty"`
}

// BuildOverride represents build configuration overrides.
type BuildOverride struct {
	Dockerfile         string            `json:"dockerfile,omitempty"`
	Context            string            `json:"context,omitempty"`
	Target             string            `json:"target,omitempty"`
	Args               map[string]string `json:"args,omitempty"`
	AdditionalContexts map[string]string `json:"additional_contexts,omitempty"`
}

// NewComposeOverride creates a new empty override.
func NewComposeOverride() *ComposeOverride {
	return &ComposeOverride{
		Services: make(map[string]*ServiceOverride),
	}
}

// Service returns (or creates) the ServiceOverride for the given name.
func (o *ComposeOverride) Service(name string) *ServiceOverride {
	if svc, ok := o.Services[name]; ok {
		return svc
	}
	svc := &ServiceOverride{}
	o.Services[name] = svc
	return svc
}

// AddVolume declares a named volume at the top level.
func (o *ComposeOverride) AddVolume(name string) {
	if o.Volumes == nil {
		o.Volumes = make(map[string]interface{})
	}
	o.Volumes[name] = struct{}{}
}

// AddEnv adds an environment variable to the service, with proper escaping
// for Docker Compose: \n → \n literal, $ → $$
func (s *ServiceOverride) AddEnv(key, value string) {
	escaped := value
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "$", "$$")
	s.Environment = append(s.Environment, key+"="+escaped)
}

// AddVolume adds a volume mount spec to the service.
func (s *ServiceOverride) AddVolume(spec string) {
	s.VolumesSpec = append(s.VolumesSpec, spec)
}

// AddLabel adds a "key=value" label to the service, escaping $ as $$ so Docker
// Compose does not interpolate it (matching AddEnv).
func (s *ServiceOverride) AddLabel(label string) {
	s.Labels = append(s.Labels, strings.ReplaceAll(label, "$", "$$"))
}

// SetPrivileged sets the privileged flag.
func (s *ServiceOverride) SetPrivileged(v bool) {
	s.Privileged = &v
}

// SetInit sets the init flag.
func (s *ServiceOverride) SetInit(v bool) {
	s.Init = &v
}

// MarshalJSON produces valid Docker Compose JSON.
func (o *ComposeOverride) MarshalJSON() ([]byte, error) {
	type alias ComposeOverride
	return json.MarshalIndent((*alias)(o), "", "  ")
}

// WriteFile writes the override to a JSON file that Docker Compose v2 can read.
func (o *ComposeOverride) WriteFile(path string) error {
	data, err := o.MarshalJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// BuildEntrypointScript generates the /bin/sh -c script that chains
// feature entrypoints (e.g., docker-init.sh) with a sleep loop.
// Matches the TS CLI pattern in src/spec-node/dockerCompose.ts:549-553.
func BuildEntrypointScript(customEntrypoints []string) string {
	var parts []string
	parts = append(parts, "echo Container started")
	parts = append(parts, `trap "exit 0" 15`)
	parts = append(parts, customEntrypoints...)
	parts = append(parts, `exec "$@"`)
	parts = append(parts, `while sleep 1 & wait $!; do :; done`)
	return strings.Join(parts, "\n")
}

// BuildEntrypointScriptCompose generates the entrypoint script for compose,
// where $ must be escaped as $$ for compose interpolation.
func BuildEntrypointScriptCompose(customEntrypoints []string) string {
	var parts []string
	parts = append(parts, "echo Container started")
	parts = append(parts, `trap "exit 0" 15`)
	parts = append(parts, customEntrypoints...)
	parts = append(parts, `exec "$$@"`)
	parts = append(parts, `while sleep 1 & wait $$!; do :; done`)
	return strings.Join(parts, "\n")
}
