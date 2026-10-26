package oci

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// TestTSOracleAuthPolicy asserts the cross-origin auth host parsing and the
// token realm check against the reference functions' results (go tool task
// testdata:ts-oracle): authorities go through Node's WHATWG URL parser, so
// default ports drop, hosts normalize and a path resolving to "/" is accepted.
func TestTSOracleAuthPolicy(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/authpolicy.json", map[string]oracletest.Func{
		"parseCrossOriginAuthHosts": func(args []json.RawMessage) (any, error) {
			var entries []string
			if err := oracletest.Args(args, &entries); err != nil {
				return nil, err
			}
			hosts, err := ParseCrossOriginAuthHosts(entries)
			if err != nil {
				return nil, err
			}
			out := map[string][]string{}
			for registry, authHosts := range hosts {
				for authHost := range authHosts {
					out[registry] = append(out[registry], authHost)
				}
				slices.Sort(out[registry])
			}
			return out, nil
		},
		// TS returns false when either URL fails to parse.
		"isAllowedTokenServiceRealm": func(args []json.RawMessage) (any, error) {
			var realm, registry string
			var entries []string
			if err := oracletest.Args(args, &realm, &registry, &entries); err != nil {
				return nil, err
			}
			policy, err := NewAuthPolicy(false, entries, log.Null)
			if err != nil {
				return false, nil
			}
			realmURL, rerr := url.Parse(realm)
			registryURL, gerr := url.Parse(registry)
			if rerr != nil || gerr != nil {
				return false, nil
			}
			return policy.allowsTokenServiceRealm(realmURL, registryURL), nil
		},
	})
}
