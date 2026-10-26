package oci

import "testing"

// The reference speaks plain HTTP only to a registry whose host is localhost
// (getRegistryScheme, containerCollectionsOCI.ts); a loopback IP literal gets
// HTTPS like any other registry. The transport must follow the same scheme the
// read-configuration output reports (Ref.Scheme).
func TestRepositoryPlainHTTPOnlyForLocalhost(t *testing.T) {
	for registry, want := range map[string]bool{
		"localhost":                true,
		"localhost:5000":           true,
		"127.0.0.1:5000":           false,
		"[::1]:5000":               false,
		"ghcr.io":                  false,
		"registry.example.com:443": false,
	} {
		ref, err := ParseRef(registry + "/owner/features/f:1")
		if err != nil {
			t.Fatalf("%s: %v", registry, err)
		}
		repo, err := (&Client{}).repository(ref)
		if err != nil {
			t.Fatalf("%s: %v", registry, err)
		}
		if repo.PlainHTTP != want {
			t.Errorf("%s: PlainHTTP = %v, want %v (scheme %q)", registry, repo.PlainHTTP, want, ref.Scheme())
		}
	}
}
