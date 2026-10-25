package cli

import (
	"os"
	"strings"

	"github.com/devcontainers/cli/internal/config"
)

func resolveContainerVariables(containerEnv map[string]string, value any) (any, error) {
	return config.NewVariableResolver().AfterContainer(config.SubstitutionContext{
		Platform:     "linux",
		ContainerEnv: containerEnv,
	}, value)
}

// osEnvMap returns the current process environment as a map.
func osEnvMap() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		if before, after, ok := strings.Cut(e, "="); ok {
			env[before] = after
		}
	}
	return env
}

// envSliceToMap converts Docker's []string{"KEY=VALUE"} to map[string]string.
func envSliceToMap(envs []string) map[string]string {
	m := make(map[string]string)
	for _, e := range envs {
		if before, after, ok := strings.Cut(e, "="); ok {
			m[before] = after
		}
	}
	return m
}
