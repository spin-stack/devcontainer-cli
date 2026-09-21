package oci

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestParseCrossOriginAuthHosts(t *testing.T) {
	hosts, err := ParseCrossOriginAuthHosts([]string{"Registry.Example=Auth.Example:8443"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !hosts["registry.example"]["auth.example:8443"] {
		t.Fatalf("mapping not registered: %v", hosts)
	}

	// Rejections mirror the reference CLI's messages, which the CLI surfaces verbatim.
	for _, tc := range []struct{ entry, want string }{
		{"bad", "Invalid cross-origin auth host 'bad'. Expected '<registry-host>=<auth-host>'."},
		{"=auth.example", "Invalid cross-origin auth host '=auth.example'. Expected '<registry-host>=<auth-host>'."},
		{"registry.example=", "Invalid cross-origin auth host 'registry.example='. Expected '<registry-host>=<auth-host>'."},
		{"a=b=c", "Invalid cross-origin auth host 'a=b=c'. Expected '<registry-host>=<auth-host>'."},
		{"a/b=c", "Invalid authority 'a/b'."},
		{"a=b/c", "Invalid authority 'b/c'."},
		{"user@a=b", "Invalid authority 'user@a'."},
	} {
		_, err := ParseCrossOriginAuthHosts([]string{tc.entry})
		if err == nil || err.Error() != tc.want {
			t.Errorf("entry %q: got error %v, want %q", tc.entry, err, tc.want)
		}
	}
}

func TestAllowsTokenServiceRealm(t *testing.T) {
	policy, err := NewAuthPolicy(true, []string{"registry.example=auth.example"}, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	cases := []struct {
		name          string
		realm, regist string
		want          bool
	}{
		{"same authority https", "https://ghcr.io/token", "https://ghcr.io", true},
		{"same authority http on localhost", "http://localhost:5000/token", "http://localhost:5000", true},
		{"same authority http off localhost", "http://registry.example/token", "http://registry.example", false},
		{"built-in docker hub mapping", "https://auth.docker.io/token", "https://registry-1.docker.io", true},
		{"built-in gitlab mapping", "https://gitlab.com/jwt/auth", "https://registry.gitlab.com", true},
		{"configured mapping", "https://auth.example/token", "https://registry.example", true},
		{"configured mapping is not symmetric", "https://registry.example/token", "https://auth.example", false},
		{"untrusted cross origin", "https://evil.example/token", "https://ghcr.io", false},
		{"cross origin over http", "http://auth.example/token", "https://registry.example", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.allowsTokenServiceRealm(mustURL(t, tc.realm), mustURL(t, tc.regist))
			if got != tc.want {
				t.Errorf("allowsTokenServiceRealm(%q, %q) = %v, want %v", tc.realm, tc.regist, got, tc.want)
			}
		})
	}
}

func TestIsRegistryOriginTreatsDockerHubAuthoritiesAsOne(t *testing.T) {
	if !isRegistryOrigin(mustURL(t, "https://registry-1.docker.io"), mustURL(t, "https://docker.io")) {
		t.Error("Docker Hub authorities should compare as the same origin")
	}
	if isRegistryOrigin(mustURL(t, "https://ghcr.io"), mustURL(t, "https://docker.io")) {
		t.Error("unrelated registries must not compare as the same origin")
	}
	if !isRegistryOrigin(mustURL(t, "https://ghcr.io:443"), mustURL(t, "https://ghcr.io")) {
		t.Error("the default port must be normalized")
	}
}

// roundTrip drives the policy transport against a fake registry response.
func roundTrip(t *testing.T, policy *AuthPolicy, registryScheme, registryHost string, handler http.HandlerFunc, requestPath string) (*http.Response, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	host := registryHost
	if host == "" {
		host = strings.TrimPrefix(srv.URL, "http://")
	}
	transport := newAuthPolicyTransport(http.DefaultTransport, policy, registryScheme, host)
	req, err := http.NewRequest(http.MethodGet, srv.URL+requestPath, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return transport.RoundTrip(req)
}

func TestAuthLookupDiagnosticAndHardening(t *testing.T) {
	challenge := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://evil.example/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}

	// Hardening off: the untrusted realm is only reported, the response passes through.
	policy, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	resp, err := roundTrip(t, policy, "http", "", challenge, "/v2/x/manifests/1")
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if diag := policy.Diagnostics(); !diag.AuthLookupWouldBeBlocked {
		t.Errorf("authLookupWouldBeBlocked not recorded: %+v", diag)
	}

	// Hardening on: the same challenge fails the request instead.
	hardened, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if _, err := roundTrip(t, hardened, "http", "", challenge, "/v2/x/manifests/1"); err == nil {
		t.Fatal("hardened round trip should fail on an untrusted realm")
	} else if !strings.Contains(err.Error(), "untrusted realm") ||
		!strings.Contains(err.Error(), "--allow-cross-origin-auth-host") {
		t.Errorf("error = %v, want the untrusted-realm error with the allow hint", err)
	}
	if diag := hardened.Diagnostics(); !diag.AuthLookupWouldBeBlocked {
		t.Errorf("authLookupWouldBeBlocked not recorded under hardening: %+v", diag)
	}
}

func TestSameAuthorityRealmIsAllowedAndNotReported(t *testing.T) {
	policy, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	var srvHost string
	handler := func(w http.ResponseWriter, r *http.Request) {
		srvHost = r.Host
		w.Header().Set("WWW-Authenticate", `Bearer realm="http://localhost:`+strings.SplitN(r.Host, ":", 2)[1]+`/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}
	// httptest serves on 127.0.0.1; the realm points at localhost on the same port,
	// so force the registry host to the localhost form the realm uses.
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()
	port := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)[1]
	transport := newAuthPolicyTransport(http.DefaultTransport, policy, "http", "localhost:"+port)
	req, err := http.NewRequest(http.MethodGet, "http://localhost:"+port+"/v2/x/manifests/1", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v (host %q)", err, srvHost)
	}
	resp.Body.Close()
	if diag := policy.Diagnostics(); diag != (AuthDiagnostics{}) {
		t.Errorf("no diagnostic expected for a same-authority realm, got %+v", diag)
	}
	if !transport.isTokenEndpoint(mustURL(t, "http://localhost:"+port+"/token")) {
		t.Error("the accepted realm should be registered as a token endpoint")
	}
}

func TestRegistryRedirectDiagnostic(t *testing.T) {
	policy, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	// The challenge arrives from an origin other than the ref's registry.
	_, err = roundTrip(t, policy, "https", "ghcr.io", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}, "/v2/x/manifests/1")
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if diag := policy.Diagnostics(); !diag.RegistryRedirectWouldPreventCredentialForwarding {
		t.Errorf("registryRedirectWouldPreventCredentialForwarding not recorded: %+v", diag)
	}
}

func TestTokenEndpointRedirect(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			http.Redirect(w, r, "https://elsewhere.example/token", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}

	// Hardening off: the redirect is recorded and followed.
	policy, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	transport := newAuthPolicyTransport(http.DefaultTransport, policy, "http", host)
	transport.addTokenEndpoint(mustURL(t, srv.URL+"/token"))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/token", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()
	if diag := policy.Diagnostics(); !diag.AuthServerRedirect {
		t.Errorf("authServerRedirect not recorded: %+v", diag)
	}

	// Hardening on: a redirecting token endpoint fails the token request.
	hardened, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	transport = newAuthPolicyTransport(http.DefaultTransport, hardened, "http", host)
	transport.addTokenEndpoint(mustURL(t, srv.URL+"/token"))
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/token", nil)
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("hardened token request should fail on a redirect")
	} else if !strings.Contains(err.Error(), "redirected the token request") {
		t.Errorf("error = %v, want the token-redirect error", err)
	}
}

func TestBearerRealm(t *testing.T) {
	cases := map[string]string{
		`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:x:pull"`: "https://ghcr.io/token",
		`bearer service="x", realm="https://a.example/token"`:                              "https://a.example/token",
		`Basic realm="https://ghcr.io/token"`:                                              "",
		`Bearer service="x"`:                                                               "",
		``:                                                                                 "",
	}
	for challenge, want := range cases {
		if got := bearerRealm(challenge); got != want {
			t.Errorf("bearerRealm(%q) = %q, want %q", challenge, got, want)
		}
	}
}

func TestRefScheme(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/devcontainers/features/go:1": "https",
		"localhost:5000/features/go:1":        "http",
		"127.0.0.1:5000/features/go:1":        "https",
	}
	for input, want := range cases {
		ref, err := ParseRef(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		if got := ref.Scheme(); got != want {
			t.Errorf("Scheme(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestClientHonorsAuthPolicyEndToEnd drives the policy through the real client:
// a registry that points its bearer challenge at an untrusted origin must fail the
// fetch under hardening, and must be reported (but tolerated) without it.
func TestClientHonorsAuthPolicyEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://evil.example/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ref, err := ParseRef(strings.TrimPrefix(srv.URL, "http://") + "/devcontainers/features/go:1")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	hardened, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if _, err := NewClientWithAuthPolicy(log.Null, map[string]string{"DOCKER_CONFIG": t.TempDir()}, hardened).FetchManifest(ref, ""); err == nil {
		t.Error("fetch should fail when the registry points at an untrusted realm")
	}
	if diag := hardened.Diagnostics(); !diag.AuthLookupWouldBeBlocked {
		t.Errorf("authLookupWouldBeBlocked not recorded through the client: %+v", diag)
	}

	// Without hardening the same challenge is only reported; the failure that
	// follows is the ordinary auth failure, not the policy refusing the realm.
	policy, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	_, _ = NewClientWithAuthPolicy(log.Null, map[string]string{"DOCKER_CONFIG": t.TempDir()}, policy).FetchManifest(ref, "")
	if diag := policy.Diagnostics(); !diag.AuthLookupWouldBeBlocked {
		t.Errorf("authLookupWouldBeBlocked not recorded with hardening off: %+v", diag)
	}
}
