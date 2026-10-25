package docker

import (
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// TestTSOracleDockerfile asserts EnsureFinalStageName, FindBaseImage and
// isBuildxCacheToInline against the reference functions' results
// (go tool task testdata:ts-oracle).
func TestTSOracleDockerfile(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/dockerfile.json", map[string]oracletest.Func{
		"ensureDockerfileHasFinalStageName": func(args []json.RawMessage) (any, error) {
			var content, name string
			if err := oracletest.Args(args, &content, &name); err != nil {
				return nil, err
			}
			stage, modified := EnsureFinalStageName(content, name)
			out := map[string]string{"lastStageName": stage}
			if modified != "" {
				out["modifiedDockerfile"] = modified
			}
			return out, nil
		},
		"findBaseImage": func(args []json.RawMessage) (any, error) {
			var content, target string
			var buildArgs, platformArgs map[string]string
			if err := oracletest.Args(args, &content, &buildArgs, &target, &platformArgs); err != nil {
				return nil, err
			}
			return oracletest.OptionalString(FindBaseImage(ExtractDockerfile(content), buildArgs, target)), nil
		},
		"isBuildxCacheToInline": func(args []json.RawMessage) (any, error) {
			var cacheTo string
			if err := oracletest.Args(args, &cacheTo); err != nil {
				return nil, err
			}
			return isBuildxCacheToInline(cacheTo), nil
		},
	})
}
