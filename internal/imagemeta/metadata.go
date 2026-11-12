package imagemeta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/devcontainers/cli/internal/log"
)

const MetadataLabel = "devcontainer.metadata"

type Entry struct {
	ID                   string             `json:"id,omitempty"`
	Init                 *bool              `json:"init,omitempty"`
	Privileged           *bool              `json:"privileged,omitempty"`
	CapAdd               []string           `json:"capAdd,omitempty"`
	SecurityOpt          []string           `json:"securityOpt,omitempty"`
	Mounts               []any              `json:"mounts,omitempty"`
	ContainerEnv         map[string]string  `json:"containerEnv,omitempty"`
	ContainerUser        string             `json:"containerUser,omitempty"`
	RemoteEnv            map[string]*string `json:"remoteEnv,omitempty"`
	RemoteUser           string             `json:"remoteUser,omitempty"`
	UpdateRemoteUserUID  *bool              `json:"updateRemoteUserUID,omitempty"`
	UserEnvProbe         string             `json:"userEnvProbe,omitempty"`
	OverrideCommand      *bool              `json:"overrideCommand,omitempty"`
	ForwardPorts         []any              `json:"forwardPorts,omitempty"`
	PortsAttributes      map[string]any     `json:"portsAttributes,omitempty"`
	OtherPortsAttributes any                `json:"otherPortsAttributes,omitempty"`
	OnCreateCommand      any                `json:"onCreateCommand,omitempty"`
	UpdateContentCommand any                `json:"updateContentCommand,omitempty"`
	PostCreateCommand    any                `json:"postCreateCommand,omitempty"`
	PostStartCommand     any                `json:"postStartCommand,omitempty"`
	PostAttachCommand    any                `json:"postAttachCommand,omitempty"`
	WaitFor              string             `json:"waitFor,omitempty"`
	ShutdownAction       string             `json:"shutdownAction,omitempty"`
	HostRequirements     any                `json:"hostRequirements,omitempty"`
	Customizations       map[string]any     `json:"customizations,omitempty"`
	Entrypoint           string             `json:"entrypoint,omitempty"`
}

var featureEntryKeys = []string{
	"id", "onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"init", "privileged", "capAdd", "securityOpt", "entrypoint", "mounts", "customizations",
}

var configEntryKeys = []string{
	"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"waitFor", "customizations", "mounts", "containerEnv", "containerUser", "init", "privileged", "capAdd",
	"securityOpt", "remoteUser", "userEnvProbe", "remoteEnv", "overrideCommand", "portsAttributes",
	"otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
	"entrypoint",
}

func (e Entry) MarshalJSON() ([]byte, error) {
	type plain Entry
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(plain(e)); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data.Bytes(), &fields); err != nil {
		return nil, err
	}
	order := configEntryKeys
	if e.ID != "" {
		order = append(slices.Clone(featureEntryKeys), configEntryKeys...)
	}
	var out bytes.Buffer
	out.WriteByte('{')
	written := 0
	for _, k := range order {
		v, ok := fields[k]
		if !ok {
			continue
		}
		delete(fields, k)
		if written > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, "%q:%s", k, v)
		written++
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func ReadMetadataFromLabels(labels map[string]string, logger log.Logger) []Entry {
	raw, ok := labels[MetadataLabel]
	if !ok || raw == "" {
		return nil
	}

	var entries []Entry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		var single Entry
		if err2 := json.Unmarshal([]byte(raw), &single); err2 != nil {
			logger.Write("Failed to parse image metadata label: "+err.Error(), log.LevelWarning)
			return nil
		}
		entries = []Entry{single}
	}
	return entries
}

func GenerateMetadataLabel(entries []Entry) string {
	entries = nonEmptyEntries(entries)
	if len(entries) == 0 {
		return ""
	}
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(entries)
	return string(bytes.TrimSuffix(data.Bytes(), []byte("\n")))
}
