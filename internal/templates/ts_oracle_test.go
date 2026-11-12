package templates

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

func TestTemplateOptionsAgainstTSOracle(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/templates.json", map[string]oracletest.Func{
		"replaceTemplatedValues": func(args []json.RawMessage) (any, error) {
			var template string
			var options map[string]string
			if err := oracletest.Args(args, &template, &options); err != nil {
				return nil, err
			}
			return substituteTemplateOptions(template, options), nil
		},
	})
}
