package config

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// tsSubstitutionContext is a TS SubstitutionContext as the golden writes it.
type tsSubstitutionContext struct {
	Platform                 string            `json:"platform"`
	ConfigFile               string            `json:"configFile"`
	LocalWorkspaceFolder     string            `json:"localWorkspaceFolder"`
	ContainerWorkspaceFolder string            `json:"containerWorkspaceFolder"`
	Env                      map[string]string `json:"env"`
}

// TestTSOracleVariableSubstitution asserts varsub.go against the reference
// functions' results (go tool task testdata:ts-oracle). A divergence in
// ${devcontainerId} would break every use of it in up/build/exec, and the
// parity matrix cannot see it: each side runs with its own --id-labels.
func TestTSOracleVariableSubstitution(t *testing.T) {
	resolve := func(ctx SubstitutionContext, phase SubstitutionPhase, value any) (any, error) {
		got, err := NewVariableResolver().Resolve(ctx, phase, value)
		if err != nil {
			return nil, err
		}
		return got, nil
	}
	oracletest.Run(t, "testdata/ts-oracle/varsub.json", map[string]oracletest.Func{
		"devcontainerIdForLabels": func(args []json.RawMessage) (any, error) {
			var labels map[string]string
			if err := oracletest.Args(args, &labels); err != nil {
				return nil, err
			}
			return ComputeDevContainerID(labels), nil
		},
		"substitute": func(args []json.RawMessage) (any, error) {
			var c tsSubstitutionContext
			var value any
			if err := oracletest.Args(args, &c, &value); err != nil {
				return nil, err
			}
			return resolve(SubstitutionContext{HostSubContext: HostSubContext{
				Platform: c.Platform, LocalWorkspaceFolder: c.LocalWorkspaceFolder,
				ContainerWorkspaceFolder: c.ContainerWorkspaceFolder, Env: c.Env, ConfigFilePath: c.ConfigFile,
			}}, PhaseHost, value)
		},
		"beforeContainerSubstitute": func(args []json.RawMessage) (any, error) {
			var labels map[string]string
			var value any
			if err := oracletest.Args(args, &labels, &value); err != nil {
				return nil, err
			}
			return resolve(SubstitutionContext{IDLabels: labels}, PhaseIdentity, value)
		},
		"containerSubstitute": func(args []json.RawMessage) (any, error) {
			var platform, configFile string
			var env map[string]string
			var value any
			if err := oracletest.Args(args, &platform, &configFile, &env, &value); err != nil {
				return nil, err
			}
			return resolve(SubstitutionContext{
				HostSubContext: HostSubContext{Platform: platform, ConfigFilePath: configFile},
				ContainerEnv:   env,
			}, PhaseContainer, value)
		},
	})
}
