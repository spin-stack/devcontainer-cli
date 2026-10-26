package imagemeta

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// TestTSOracleImageMetadata asserts the ports in merge.go and extend.go against
// the reference functions' results (go tool task testdata:ts-oracle).
func TestTSOracleImageMetadata(t *testing.T) {
	str := func(port func(string) string) oracletest.Func {
		return func(args []json.RawMessage) (any, error) {
			var s string
			if err := oracletest.Args(args, &s); err != nil {
				return nil, err
			}
			return port(s), nil
		}
	}
	oracletest.Run(t, "testdata/ts-oracle/imagemeta.json", map[string]oracletest.Func{
		"mergeConfiguration": func(args []json.RawMessage) (any, error) {
			var config map[string]any
			var entries []Entry
			if err := oracletest.Args(args, &config, &entries); err != nil {
				return nil, err
			}
			data, err := json.Marshal(MergeConfiguration(entries))
			if err != nil {
				return nil, err
			}
			// TS leaves the properties it has no value for undefined, out of its JSON.
			var merged map[string]any
			if err := json.Unmarshal(data, &merged); err != nil {
				return nil, err
			}
			for k, v := range merged {
				if v == nil {
					delete(merged, k)
				}
			}
			return merged, nil
		},
		"getDevcontainerMetadataLabel": func(args []json.RawMessage) (any, error) {
			var entries []Entry
			if err := oracletest.Args(args, &entries); err != nil {
				return nil, err
			}
			return devcontainerMetadataLabel(entries), nil
		},
		"getEntPasswdShellCommand": str(getEntPasswdShellCommand),
		"generateContainerEnvs": func(args []json.RawMessage) (any, error) {
			var env map[string]string
			var escapeDollar bool
			if err := oracletest.Args(args, &env, &escapeDollar); err != nil {
				return nil, err
			}
			return generateContainerEnvs(env, escapeDollar), nil
		},
		"getFeatureValueObject": func(args []json.RawMessage) (any, error) {
			var feat features.Feature
			if err := oracletest.Args(args, &feat); err != nil {
				return nil, err
			}
			info, err := ParseFeatureFileInfo(args[0])
			if err != nil {
				return nil, err
			}
			keys, values := featureValueObject(feat, info)
			entries := make([][2]any, len(keys))
			for i, k := range keys {
				entries[i] = [2]any{k, values[k]}
			}
			return entries, nil
		},
		"getSafeId":            str(safeID),
		"escapeQuotesForShell": str(escapeQuotesForShell),
	})
}

// JSON.stringify writes the -0 that parseInt("-0") makes as 0; so does the
// merged configuration, byte for byte (DeepEqual cannot tell -0 from 0).
func TestMergeForwardPortsWritesANegativeZeroAsZero(t *testing.T) {
	merged := MergeConfiguration([]Entry{{ForwardPorts: []any{"aaaaaaaaaa-0 localhost:1"}}})
	data, err := json.Marshal(merged.ForwardPorts)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[0]" {
		t.Errorf("forwardPorts = %s, want [0]", data)
	}
}
