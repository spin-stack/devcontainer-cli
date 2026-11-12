package cli

import (
	"encoding/json"
	"os"

	"github.com/devcontainers/cli/internal/config"
)

func resolveContainerVariables(containerEnv map[string]string, configFile string, value any) (any, error) {
	ctx := config.SubstitutionContext{ContainerEnv: containerEnv}
	ctx.ConfigFilePath = configFile
	return config.NewVariableResolver().AfterContainer(ctx, value)
}

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

func osEnvMap() map[string]string { return config.EnvMap(os.Environ()) }
