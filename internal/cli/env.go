package cli

import (
	"os"
	"strings"

	"github.com/devcontainers/cli/internal/config"
)

func resolveContainerVariables(containerEnv map[string]string, value interface{}) (interface{}, error) {
	return config.NewVariableResolver().AfterContainer(config.SubstitutionContext{
		HostSubContext: config.HostSubContext{Platform: "linux"},
		ContainerEnv:   containerEnv,
	}, value)
}

// osEnvMap returns the current process environment as a map.
func osEnvMap() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			env[e[:i]] = e[i+1:]
		}
	}
	return env
}

// envSliceToMap converts Docker's []string{"KEY=VALUE"} to map[string]string.
func envSliceToMap(envs []string) map[string]string {
	m := make(map[string]string)
	for _, e := range envs {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}
