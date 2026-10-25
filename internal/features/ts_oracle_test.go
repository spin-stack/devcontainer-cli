package features

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// The goldens are the results of the reference functions themselves
// (go tool task testdata:ts-oracle).
func TestTSOracleVersion(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/version.json", map[string]oracletest.Func{
		"parseVersion": func(args []json.RawMessage) (any, error) {
			var s string
			if err := oracletest.Args(args, &s); err != nil {
				return nil, err
			}
			return parseVersion(s), nil
		},
		"isEarlierVersion": func(args []json.RawMessage) (any, error) {
			var l, r []int
			if err := oracletest.Args(args, &l, &r); err != nil {
				return nil, err
			}
			return isEarlierVersion(l, r), nil
		},
	})
}
