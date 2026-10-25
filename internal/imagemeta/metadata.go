// Package imagemeta reads, merges and extends dev container image metadata.
package imagemeta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/devcontainers/cli/internal/log"
)

// MetadataLabel is the Docker label key used to persist devcontainer metadata.
const MetadataLabel = "devcontainer.metadata"

// Entry represents one metadata entry (from config, feature, or base image).
// Matches the TS ImageMetadataEntry.
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

// featureEntryKeys is the order TS writes a Feature's entry in: id, then
// pickFeatureProperties.
var featureEntryKeys = []string{
	"id", "onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"init", "privileged", "capAdd", "securityOpt", "entrypoint", "mounts", "customizations",
}

// configEntryKeys is the order TS writes the configuration's entry in
// (pickConfigProperties); entrypoint, which only a Feature's entry has, last.
var configEntryKeys = []string{
	"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"waitFor", "customizations", "mounts", "containerEnv", "containerUser", "init", "privileged", "capAdd",
	"securityOpt", "remoteUser", "userEnvProbe", "remoteEnv", "overrideCommand", "portsAttributes",
	"otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
	"entrypoint",
}

// MarshalJSON writes the entry's properties in the order TS writes them: a
// Feature's entry (one with an id) in pickFeatureProperties order, then the
// other properties in configEntryKeys order; any other entry in
// pickConfigProperties order (configEntryKeys names every property but id).
// Object values are written with their keys sorted (a Go map keeps no order).
// &, < and > are not escaped, as JSON.stringify does not escape them.
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
		delete(fields, k) // a Feature's order names some keys twice
		if written > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, "%q:%s", k, v)
		written++
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// ReadMetadataFromLabels extracts metadata entries from Docker image/container labels.
func ReadMetadataFromLabels(labels map[string]string, logger log.Logger) []Entry {
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
// Matches the TS getDevcontainerMetadataLabel as of 0.88 (#1199): entries are
// ALWAYS written as a JSON array — even for a single entry — and an empty slice
// yields "". ReadMetadataFromLabels still accepts a bare object for backward
// compatibility with labels written by older tooling.
func GenerateMetadataLabel(entries []Entry) string {
	entries = nonEmptyEntries(entries)
	if len(entries) == 0 {
		return ""
	}
	// 0.88 (#1199): always write the devcontainer.metadata label as a JSON array,
	// even for a single entry — tools that expect an array (e.g. Zed) fail on a
	// bare object. JSON.stringify leaves &, < and > as they are.
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(entries) // an Entry holds JSON values only
	return string(bytes.TrimSuffix(data.Bytes(), []byte("\n")))
}
