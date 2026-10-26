package cli

import (
	"encoding/json"
	"os"

	"github.com/devcontainers/cli/internal/config"
)

// resolveContainerVariables is TS containerSubstitute: ${containerEnv:…}
// resolved from the container's environment. A ${containerEnv} without a name
// is TS's ContainerError, which names configFile (the configuration's file,
// "" without one).
func resolveContainerVariables(containerEnv map[string]string, configFile string, value any) (any, error) {
	// The container's platform is not win32, so variable names are matched as
	// written (the zero Platform).
	ctx := config.SubstitutionContext{ContainerEnv: containerEnv}
	ctx.ConfigFilePath = configFile
	return config.NewVariableResolver().AfterContainer(ctx, value)
}

// checkContainerSubstitution substitutes the container's environment into
// each value (the configuration, the merged configuration) for the failure
// alone, as TS's commands run containerSubstitute over them before they use
// the container: the first error, a ContainerError, is returned.
func checkContainerSubstitution(containerEnv map[string]string, configFile string, values ...any) error {
	for _, v := range values {
		data, _ := json.Marshal(v)
		var generic any
		json.Unmarshal(data, &generic)
		if _, err := resolveContainerVariables(containerEnv, configFile, generic); err != nil {
			return err
		}
	}
	return nil
}

// osEnvMap returns the current process environment as a map.
func osEnvMap() map[string]string { return config.EnvMap(os.Environ()) }
