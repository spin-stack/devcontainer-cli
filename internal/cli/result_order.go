package cli

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/devcontainers/cli/internal/config"
)

// orderedObject writes fields as a JSON object in the order TS writes it:
// JSON.stringify follows property insertion order, which order gives. The keys
// order names come first, each once, then any other key sorted, as json.Marshal
// writes a map (a Go-only property, such as read-configuration's cacheKey).
type orderedObject struct {
	fields map[string]any
	order  []string
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(o.fields))
	for _, k := range o.order {
		if _, ok := o.fields[k]; ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(o.fields)) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		value, err := json.Marshal(o.fields[k])
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// The order of each command's result, as the TS object literal that builds it
// lists its properties.
var (
	// doProvision: outcome, then launch's result (devContainers.ts).
	upResultKeys = []string{"outcome", "containerId", "composeProjectName", "remoteUser", "remoteWorkspaceFolder", "configuration", "mergedConfiguration", "ociAuthDiagnostics"}
	// doBuild.
	buildResultKeys = []string{"outcome", "imageName", "ociAuthDiagnostics"}
	// doSetUp.
	setUpResultKeys = []string{"outcome", "configuration", "mergedConfiguration"}
	// doRunUserCommands.
	runUserCommandsResultKeys = []string{"outcome", "result"}
	// readConfiguration.
	readConfigurationKeys = []string{"configuration", "workspace", "featuresConfiguration", "mergedConfiguration", "ociAuthDiagnostics"}
	// getWorkspaceConfiguration's WorkspaceConfiguration (utils.ts).
	workspaceKeys = []string{"workspaceFolder", "workspaceMount", "additionalMountString"}
	// generateFeaturesConfig's FeaturesConfig.
	featuresConfigurationKeys = []string{"featureSets", "dstFolder"}
	// The properties mergeConfiguration (imageMetadata.ts) sets after spreading
	// the configuration without its replaceProperties.
	mergedConfigurationKeys = []string{
		"init", "privileged", "capAdd", "securityOpt", "entrypoints", "mounts", "customizations",
		"onCreateCommands", "updateContentCommands", "postCreateCommands", "postStartCommands", "postAttachCommands",
		"waitFor", "remoteUser", "containerUser", "userEnvProbe", "remoteEnv", "containerEnv", "overrideCommand",
		"portsAttributes", "otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
	}
	// mergeConfiguration's replaceProperties, which it deletes from the spread
	// configuration.
	mergedReplacedKeys = []string{
		"customizations", "entrypoint", "onCreateCommand", "updateContentCommand", "postCreateCommand",
		"postStartCommand", "postAttachCommand", "shutdownAction",
	}
)

// configurationKeys is the order of a configuration's properties: its
// document's, then customizations, which updateFromOldProperties adds when it
// moves a deprecated property, then the configFilePath TS sets last.
func configurationKeys(result *config.LoadResult) []string {
	var keys []string
	if result != nil {
		keys = slices.Clone(result.KeyOrder)
	}
	return append(keys, "customizations", "configFilePath")
}

// mergedKeys is the order of a merged configuration's properties: the
// configuration's, without the ones mergeConfiguration replaces, then the ones
// it sets; a property the configuration already has keeps its place, as in a
// JavaScript object literal.
func mergedKeys(configurationKeys []string) []string {
	keys := slices.DeleteFunc(slices.Clone(configurationKeys), func(k string) bool {
		return slices.Contains(mergedReplacedKeys, k)
	})
	return append(keys, mergedConfigurationKeys...)
}

// orderConfigurations gives result's configuration and mergedConfiguration,
// when it has them as objects, their TS order.
func orderConfigurations(result map[string]any, loaded *config.LoadResult) {
	cfgKeys := configurationKeys(loaded)
	if m, ok := result["configuration"].(map[string]any); ok {
		result["configuration"] = orderedObject{m, cfgKeys}
	}
	if m, ok := result["mergedConfiguration"].(map[string]any); ok {
		result["mergedConfiguration"] = orderedObject{m, mergedKeys(cfgKeys)}
	}
}
