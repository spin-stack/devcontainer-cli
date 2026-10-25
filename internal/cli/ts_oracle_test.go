package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// The ts-oracle goldens are the results of the reference functions
// themselves (go tool task testdata:ts-oracle).

func tsFindBaseImage(args []json.RawMessage) (any, error) {
	var content, target string
	var buildArgs, platformArgs map[string]string
	if err := oracletest.Args(args, &content, &buildArgs, &target, &platformArgs); err != nil {
		return nil, err
	}
	r := docker.NewResolver(docker.ExtractDockerfile(content), buildArgs, platformArgs)
	return oracletest.OptionalString(r.FindBaseImage(target)), nil
}

func TestTSOracleDockerfile(t *testing.T) {
	impls := map[string]oracletest.Func{
		"findBaseImage": tsFindBaseImage,
		"findUserStatement": func(args []json.RawMessage) (any, error) {
			var content, target string
			var buildArgs, baseEnv, platformArgs map[string]string
			if err := oracletest.Args(args, &content, &buildArgs, &baseEnv, &platformArgs, &target); err != nil {
				return nil, err
			}
			r := docker.NewResolver(docker.ExtractDockerfile(content), buildArgs, platformArgs)
			return oracletest.OptionalString(r.FindUserStatement(baseEnv, target)), nil
		},
		"supportsBuildContexts": func(args []json.RawMessage) (any, error) {
			var content string
			if err := oracletest.Args(args, &content); err != nil {
				return nil, err
			}
			// TS answers true, false or 'unknown'; every caller tests it for truthiness.
			return oracletest.Truthiness(dockerfileSupportsBuildContexts(docker.ExtractDockerfile(content))), nil
		},
		// internal/docker's test asserts these.
		"ensureDockerfileHasFinalStageName": nil,
		"isBuildxCacheToInline":             nil,
	}
	oracletest.Run(t, "testdata/ts-oracle/dockerfile.json", impls)
	// The same findBaseImage cases internal/docker's FindBaseImage answers.
	oracletest.Run(t, "../docker/testdata/ts-oracle/dockerfile.json", impls)
}

func TestTSOracleCompose(t *testing.T) {
	// mount is a TS Mount or mount string, as composeMountsFromMetadata takes
	// the merged configuration's mounts.
	mount := func(args []json.RawMessage) (composeMount, error) {
		var m any
		if err := oracletest.Args(args, &m); err != nil {
			return composeMount{}, err
		}
		mounts, err := composeMountsFromMetadata([]any{m}, func(s string) (string, error) { return s, nil })
		if err != nil {
			return composeMount{}, err
		}
		return mounts[0], nil
	}
	oracletest.Run(t, "testdata/ts-oracle/compose.json", map[string]oracletest.Func{
		"getFeatureIdWithoutVersion": func(args []json.RawMessage) (any, error) {
			var id string
			if err := oracletest.Args(args, &id); err != nil {
				return nil, err
			}
			return featureIDWithoutVersion(id), nil
		},
		"readVersionPrefix": func(args []json.RawMessage) (any, error) {
			var contents []string
			if err := oracletest.Args(args, &contents); err != nil {
				return nil, err
			}
			dir := t.TempDir()
			files := make([]string, len(contents))
			for i, c := range contents {
				files[i] = filepath.Join(dir, strconv.Itoa(i)+".yml")
				if err := os.WriteFile(files[i], []byte(c), 0o644); err != nil {
					return nil, err
				}
			}
			return readComposeVersionPrefix(files)
		},
		"convertMountToVolume": func(args []json.RawMessage) (any, error) {
			m, err := mount(args)
			if err != nil {
				return nil, err
			}
			return composeVolumeSpec(m), nil
		},
		"convertMountToVolumeTopLevelElement": func(args []json.RawMessage) (any, error) {
			m, err := mount(args)
			if err != nil {
				return nil, err
			}
			return composeTopLevelVolume(m), nil
		},
	})
}
