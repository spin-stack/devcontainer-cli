package cli

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/devcontainers/cli/internal/config"
)

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

var (
	upResultKeys              = []string{"outcome", "containerId", "composeProjectName", "remoteUser", "remoteWorkspaceFolder", "configuration", "mergedConfiguration", "ociAuthDiagnostics"}
	buildResultKeys           = []string{"outcome", "imageName", "ociAuthDiagnostics"}
	setUpResultKeys           = []string{"outcome", "configuration", "mergedConfiguration"}
	runUserCommandsResultKeys = []string{"outcome", "result"}
	readConfigurationKeys     = []string{"configuration", "workspace", "featuresConfiguration", "mergedConfiguration", "ociAuthDiagnostics"}
	workspaceKeys             = []string{"workspaceFolder", "workspaceMount", "additionalMountString"}
	featuresConfigurationKeys = []string{"featureSets", "dstFolder"}
	mergedConfigurationKeys   = []string{
		"init", "privileged", "capAdd", "securityOpt", "entrypoints", "mounts", "customizations",
		"onCreateCommands", "updateContentCommands", "postCreateCommands", "postStartCommands", "postAttachCommands",
		"waitFor", "remoteUser", "containerUser", "userEnvProbe", "remoteEnv", "containerEnv", "overrideCommand",
		"portsAttributes", "otherPortsAttributes", "forwardPorts", "shutdownAction", "updateRemoteUserUID", "hostRequirements",
	}
	mergedReplacedKeys = []string{
		"customizations", "entrypoint", "onCreateCommand", "updateContentCommand", "postCreateCommand",
		"postStartCommand", "postAttachCommand", "shutdownAction",
	}
)

func configurationKeys(result *config.LoadResult) []string {
	var keys []string
	if result != nil {
		keys = slices.Clone(result.KeyOrder)
	}
	return append(keys, "customizations", "configFilePath")
}

func mergedKeys(configurationKeys []string) []string {
	keys := slices.DeleteFunc(slices.Clone(configurationKeys), func(k string) bool {
		return slices.Contains(mergedReplacedKeys, k)
	})
	return append(keys, mergedConfigurationKeys...)
}

func orderConfigurations(result map[string]any, loaded *config.LoadResult) {
	cfgKeys := configurationKeys(loaded)
	if m, ok := result["configuration"].(map[string]any); ok {
		result["configuration"] = orderedObject{m, cfgKeys}
	}
	if m, ok := result["mergedConfiguration"].(map[string]any); ok {
		result["mergedConfiguration"] = orderedObject{m, mergedKeys(cfgKeys)}
	}
}
