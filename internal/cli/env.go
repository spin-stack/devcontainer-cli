package cli

import (
	"os"

	"github.com/devcontainers/cli/internal/config"
)

func resolveContainerVariables(containerEnv map[string]string, value any) (any, error) {
	return config.NewVariableResolver().AfterContainer(config.SubstitutionContext{
		Platform:     "linux",
		ContainerEnv: containerEnv,
	}, value)
}

// osEnvMap returns the current process environment as a map.
func osEnvMap() map[string]string { return config.EnvMap(os.Environ()) }
