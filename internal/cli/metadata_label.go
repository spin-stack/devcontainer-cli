package cli

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

// labelConfigProperties is TS pickConfigProperties: what of devcontainer.json
// the devcontainer.metadata label records, in that order.
var labelConfigProperties = []string{
	"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand",
	"waitFor", "customizations", "mounts", "containerEnv", "containerUser", "init", "privileged", "capAdd",
	"securityOpt", "remoteUser", "userEnvProbe", "remoteEnv", "overrideCommand", "portsAttributes",
	"otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
}

// configLabelEntry is the configuration's entry in the devcontainer.metadata
// label: TS pick(config.raw, pickConfigProperties), the properties as written
// with their variables unresolved (raw is config.LoadResult.RawConfig). The
// merged configuration reads the substituted entry instead (configMetadataEntry).
// omitRemoteEnv is --omit-config-remote-env-from-metadata.
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

// containerMetadataLabel is the devcontainer.metadata label TS adds to a
// container created from an image with no Features: JSON.stringify of the
// entries, "[]" without any.
func containerMetadataLabel(entries []imagemeta.Entry) string {
	if label := imagemeta.GenerateMetadataLabel(entries); label != "" {
		return label
	}
	return "[]"
}

// imageLabelArgs are the --label values that put the metadata label on a built
// image: none without entries, where TS's Dockerfile has no LABEL line.
func imageLabelArgs(entries []imagemeta.Entry) []string {
	if label := imagemeta.GenerateMetadataLabel(entries); label != "" {
		return []string{imagemeta.MetadataLabel + "=" + label}
	}
	return nil
}

// metadataSubstitution resolves the variables of metadata entries read from a
// devcontainer.metadata label, which records them as written (TS
// SubstitutedConfig.substitute applied to the label's raw entries).
type metadataSubstitution func(entries *[]imagemeta.Entry) error

// hostMetadataSubstitution substitutes the host variables (${localEnv:…},
// ${localWorkspaceFolder}, …) of hostSub and, with idLabels, ${devcontainerId}.
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

// imageMetadataFromContainer is TS getImageMetadataFromContainer(...).config:
// the entries of the container's devcontainer.metadata label (its own, or its
// image's, which the container inherits) with their variables substituted.
// For a container that carries every id label, which the CLI created with the
// configuration's entry in its metadata, the configuration's updateable
// properties (TS pickUpdateableConfigProperties: remoteUser, userEnvProbe,
// remoteEnv) follow; for any other, the features entries and the
// configuration's entry. A container without the
// label has those alone. cfg nil adds nothing of a configuration.
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

// containerIDLabels is TS findContainerAndIdLabels' idLabels for a container
// found without the CLI's own lookup: the --id-label values, else the
// workspace folder's and the config file's labels, the config file's only when
// the container has one (an older container has the workspace folder's alone).
// A missing workspace folder or config file is written as TS's template
// literal writes undefined.
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

// hasEveryLabel reports whether labels has every "name=value" of idLabels (TS
// hasIdLabels: every name is set); false for nil id labels (none known).
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
