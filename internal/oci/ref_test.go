package oci

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// tsRef is a TS OCIRef: tag is undefined (out of the JSON) for a digest
// reference, digest for any other.
func tsRef(r *Ref) map[string]string {
	out := map[string]string{
		"scheme": r.Scheme(), "id": r.ID, "owner": r.Owner, "namespace": r.Namespace,
		"registry": r.Registry, "resource": r.Resource, "path": r.Path, "version": r.Version,
	}
	if r.Digest != "" {
		out["digest"] = r.Digest
	} else {
		out["tag"] = r.Tag
	}
	return out
}

// TestTSOracleRef asserts ref.go and QualifyImageName against the reference
// functions' results (go tool task testdata:ts-oracle). TS returns undefined
// where ParseRef and ParseCollectionRef fail.
func TestTSOracleRef(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/ref.json", map[string]oracletest.Func{
		"getRef": func(args []json.RawMessage) (any, error) {
			var input string
			if err := oracletest.Args(args, &input); err != nil {
				return nil, err
			}
			ref, err := ParseRef(input)
			if err != nil {
				return nil, nil
			}
			return tsRef(ref), nil
		},
		"getCollectionRef": func(args []json.RawMessage) (any, error) {
			var registry, namespace string
			if err := oracletest.Args(args, &registry, &namespace); err != nil {
				return nil, err
			}
			ref, err := ParseCollectionRef(registry, namespace)
			if err != nil {
				return nil, nil
			}
			return map[string]string{
				"scheme": ref.Ref().Scheme(), "registry": ref.Registry, "path": ref.Path,
				"resource": ref.Resource, "version": ref.Version, "tag": ref.Tag,
			}, nil
		},
		"qualifyImageName": func(args []json.RawMessage) (any, error) {
			var name string
			if err := oracletest.Args(args, &name); err != nil {
				return nil, err
			}
			return QualifyImageName(name), nil
		},
	})
}

// TS getRef splits the digest on every colon and wants exactly two parts, so
// a second colon is a malformed digest, even after a valid hex.
func TestParseRefDigestWithASecondColon(t *testing.T) {
	_, err := ParseRef("ghcr.io/a/b@sha256:" + strings.Repeat("a", 64) + ":def")
	if err == nil || !strings.Contains(err.Error(), "invalid digest format") {
		t.Errorf("err = %v, want the digest format error", err)
	}
}
