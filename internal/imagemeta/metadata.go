package imagemeta

import (
	"encoding/json"

	"github.com/devcontainers/cli/internal/core/log"
)

// MetadataLabel is the Docker label key used to persist devcontainer metadata.
const MetadataLabel = "devcontainer.metadata"

// Entry represents one metadata entry (from config, feature, or base image).
// Matches the TS ImageMetadataEntry.
type Entry struct {
	ID                   string                 `json:"id,omitempty"`
	Init                 *bool                  `json:"init,omitempty"`
	Privileged           *bool                  `json:"privileged,omitempty"`
	CapAdd               []string               `json:"capAdd,omitempty"`
	SecurityOpt          []string               `json:"securityOpt,omitempty"`
	Mounts               []interface{}          `json:"mounts,omitempty"`
	RunArgs              []string               `json:"runArgs,omitempty"`
	ContainerEnv         map[string]string      `json:"containerEnv,omitempty"`
	ContainerUser        string                 `json:"containerUser,omitempty"`
	RemoteEnv            map[string]*string     `json:"remoteEnv,omitempty"`
	RemoteUser           string                 `json:"remoteUser,omitempty"`
	UpdateRemoteUserUID  *bool                  `json:"updateRemoteUserUID,omitempty"`
	UserEnvProbe         string                 `json:"userEnvProbe,omitempty"`
	OverrideCommand      *bool                  `json:"overrideCommand,omitempty"`
	ForwardPorts         []interface{}          `json:"forwardPorts,omitempty"`
	PortsAttributes      map[string]interface{} `json:"portsAttributes,omitempty"`
	OtherPortsAttributes interface{}            `json:"otherPortsAttributes,omitempty"`
	OnCreateCommand      interface{}            `json:"onCreateCommand,omitempty"`
	UpdateContentCommand interface{}            `json:"updateContentCommand,omitempty"`
	PostCreateCommand    interface{}            `json:"postCreateCommand,omitempty"`
	PostStartCommand     interface{}            `json:"postStartCommand,omitempty"`
	PostAttachCommand    interface{}            `json:"postAttachCommand,omitempty"`
	WaitFor              string                 `json:"waitFor,omitempty"`
	ShutdownAction       string                 `json:"shutdownAction,omitempty"`
	HostRequirements     interface{}            `json:"hostRequirements,omitempty"`
	Customizations       map[string]interface{} `json:"customizations,omitempty"`
	Entrypoint           string                 `json:"entrypoint,omitempty"`
}

// ReadMetadataFromLabels extracts metadata entries from Docker image/container labels.
func ReadMetadataFromLabels(labels map[string]string, logger log.Log) []Entry {
	raw, ok := labels[MetadataLabel]
	if !ok || raw == "" {
		return nil
	}

	// Try array first, then single object
	var entries []Entry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		// Try single object
		var single Entry
		if err2 := json.Unmarshal([]byte(raw), &single); err2 != nil {
			logger.Write("Failed to parse image metadata label: "+err.Error(), log.LevelWarning)
			return nil
		}
		entries = []Entry{single}
	}
	return entries
}

// GenerateMetadataLabel serializes entries to the compact JSON label format.
// Matches the TS getDevcontainerMetadataLabel: a single entry is written as a
// bare object, multiple entries as an array (empty → "").
func GenerateMetadataLabel(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	if len(entries) == 1 {
		data, _ := json.Marshal(entries[0])
		return string(data)
	}
	data, _ := json.Marshal(entries)
	return string(data)
}
