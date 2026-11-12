package features

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

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

func TestTSOracleLockfile(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/lockfile.json", map[string]oracletest.Func{
		"generateLockfileText": func(args []json.RawMessage) (any, error) {
			var input struct {
				FeatureSets []struct {
					SourceInformation struct {
						Type          string `json:"type"`
						UserFeatureID string `json:"userFeatureId"`
						TarballURI    string `json:"tarballUri"`
						FeatureRef    struct {
							Registry string `json:"registry"`
							Path     string `json:"path"`
						} `json:"featureRef"`
					} `json:"sourceInformation"`
					ComputedDigest string    `json:"computedDigest"`
					Features       []Feature `json:"features"`
				} `json:"featureSets"`
			}
			if err := oracletest.Args(args, &input); err != nil {
				return nil, err
			}
			var sets []*Set
			for _, fs := range input.FeatureSets {
				src := fs.SourceInformation
				set := &Set{Features: fs.Features, ComputedDigest: fs.ComputedDigest}
				switch src.Type {
				case "oci":
					namespace, id := path.Split(src.FeatureRef.Path)
					set.SourceInfo = &OCISource{Type: "oci", UserID: src.UserFeatureID, Registry: src.FeatureRef.Registry, Namespace: strings.TrimSuffix(namespace, "/"), ID: id}
				case "direct-tarball":
					set.SourceInfo = &TarballSource{TarballURI: src.TarballURI, UserID: src.UserFeatureID}
				}
				sets = append(sets, set)
			}
			data, err := stringifyJSON(GenerateLockfile(&Config{FeatureSets: sets}, nil))
			return string(data), err
		},
	})
}
