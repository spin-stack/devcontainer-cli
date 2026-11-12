package cli

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

var labelConfigProperties = []string{
	"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"waitFor", "customizations", "mounts", "containerEnv", "containerUser", "init", "privileged", "capAdd",
	"securityOpt", "remoteUser", "userEnvProbe", "remoteEnv", "overrideCommand", "portsAttributes",
	"otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
}

func configLabelEntry(raw map[string]any, omitRemoteEnv bool) (imagemeta.Entry, error) {
	picked := map[string]any{}
	for _, k := range labelConfigProperties {
		if v, ok := raw[k]; ok && (!omitRemoteEnv || k != "remoteEnv") {
			picked[k] = v
		}
	}
	var entry imagemeta.Entry
	data, err := json.Marshal(picked)
	if err == nil {
		err = json.Unmarshal(data, &entry)
	}
	if err != nil {
		return imagemeta.Entry{}, fmt.Errorf("devcontainer.metadata entry of the configuration: %w", err)
	}
	return entry, nil
}

func containerMetadataLabel(entries []imagemeta.Entry) string {
	if label := imagemeta.GenerateMetadataLabel(entries); label != "" {
		return label
	}
	return "[]"
}

func imageLabelArgs(entries []imagemeta.Entry) []string {
	if label := imagemeta.GenerateMetadataLabel(entries); label != "" {
		return []string{imagemeta.MetadataLabel + "=" + label}
	}
	return nil
}

type metadataSubstitution func(entries *[]imagemeta.Entry) error

func hostMetadataSubstitution(hostSub config.HostSubContext, idLabels []string) metadataSubstitution {
	return func(entries *[]imagemeta.Entry) error {
		resolver := config.NewVariableResolver()
		ctx := config.SubstitutionContext{HostSubContext: hostSub}
		if idLabels == nil {
			return resolver.ResolveInto(ctx, config.PhaseHost, entries)
		}
		ctx.DevContainerID = config.ComputeDevContainerID(config.EnvMap(idLabels))
		return resolver.BeforeContainerInto(ctx, entries)
	}
}

func imageMetadataFromContainer(labels map[string]string, cfg *config.DevContainer, features []imagemeta.Entry, idLabels []string, substitute metadataSubstitution, logger log.Logger) ([]imagemeta.Entry, error) {
	var entries []imagemeta.Entry
	hasLabel := labels[imagemeta.MetadataLabel] != ""
	if hasLabel {
		entries = imagemeta.ReadMetadataFromLabels(labels, logger)
		if err := substitute(&entries); err != nil {
			return nil, fmt.Errorf("substitute the container's metadata: %w", err)
		}
	}
	if cfg == nil {
		return entries, nil
	}
	if hasLabel && hasEveryLabel(labels, idLabels) {
		entry := configMetadataEntry(cfg, false)
		return append(entries, imagemeta.Entry{RemoteUser: entry.RemoteUser, UserEnvProbe: entry.UserEnvProbe, RemoteEnv: entry.RemoteEnv}), nil
	}
	entries = append(entries, features...)
	return append(entries, configMetadataEntry(cfg, false)), nil
}

func containerIDLabels(provided []string, containerLabels map[string]string, workspaceFolder, configFile string) []string {
	if len(provided) > 0 {
		return provided
	}
	labels := []string{"devcontainer.local_folder=" + cmp.Or(workspaceFolder, "undefined")}
	if containerLabels["devcontainer.config_file"] != "" {
		labels = append(labels, "devcontainer.config_file="+cmp.Or(configFile, "undefined"))
	}
	return labels
}

func hasEveryLabel(labels map[string]string, idLabels []string) bool {
	if idLabels == nil {
		return false
	}
	for name := range config.EnvMap(idLabels) {
		if labels[name] == "" {
			return false
		}
	}
	return true
}
