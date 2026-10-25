package oci

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func untrustedChallenge(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="https://evil.example/token",service="registry"`)
	w.WriteHeader(http.StatusUnauthorized)
}

const blockedLine = "[httpOci] OCI auth diagnostics: Authentication lookup from registry"

// TS recordOCIAuthDiagnostic (httpOCIRegistry.ts) writes
// `[httpOci] OCI auth diagnostics: <message>` to the command's output; Go
// writes it to the logger the policy was built with, or the one SetLogger
// handed it later, and a nil logger silences it.
func TestAuthPolicyLogsDiagnosticsToItsLogger(t *testing.T) {
	t.Run("the logger the policy was built with", func(t *testing.T) {
		var buf bytes.Buffer
		policy, err := NewAuthPolicy(false, nil, log.New(log.Options{Writer: &buf, Level: log.LevelInfo}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := roundTrip(t, policy, "http", "", untrustedChallenge, "/v2/x/manifests/1"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), blockedLine) || !strings.Contains(buf.String(), "to realm origin 'https://evil.example' would be blocked by OCI auth hardening.") {
			t.Fatalf("log = %q", buf.String())
		}
	})

	t.Run("no logger", func(t *testing.T) {
		policy, err := NewAuthPolicy(false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := roundTrip(t, policy, "http", "", untrustedChallenge, "/v2/x/manifests/1"); err != nil {
			t.Fatal(err)
		}
		if !policy.Diagnostics().AuthLookupWouldBeBlocked {
			t.Fatal("diagnostic not recorded")
		}
	})

	t.Run("the logger SetLogger hands it", func(t *testing.T) {
		var first, second bytes.Buffer
		policy, err := NewAuthPolicy(false, nil, log.New(log.Options{Writer: &first, Level: log.LevelInfo}))
		if err != nil {
			t.Fatal(err)
		}
		policy.SetLogger(log.New(log.Options{Writer: &second, Level: log.LevelInfo}))
		policy.SetLogger(nil) // keeps the logger it had
		var none *AuthPolicy
		none.SetLogger(log.Null) // a nil policy ignores it
		if _, err := roundTrip(t, policy, "http", "", untrustedChallenge, "/v2/x/manifests/1"); err != nil {
			t.Fatal(err)
		}
		if first.Len() != 0 || !strings.Contains(second.String(), blockedLine) {
			t.Fatalf("first logger %q, second logger %q", first.String(), second.String())
		}
	})
}

// TS isOCIRegistryOrigin compares URL.origin, which keeps a non-default port,
// and treats the Docker Hub authorities as one origin only when both URLs are
// HTTPS and both hosts are Docker Hub's.
func TestIsRegistryOriginConditions(t *testing.T) {
	for _, tt := range []struct {
		u, registry string
		want        bool
	}{
		{"https://ghcr.io:5000", "https://ghcr.io", false},
		{"http://localhost:80", "http://localhost", true},
		{"http://docker.io", "https://index.docker.io", false},
		{"https://docker.io", "http://index.docker.io", false},
		{"https://ghcr.io", "https://docker.io", false},
		{"https://docker.io", "https://ghcr.io", false},
		{"https://docker.io", "https://index.docker.io", true},
	} {
		if got := isRegistryOrigin(mustURL(t, tt.u), mustURL(t, tt.registry)); got != tt.want {
			t.Errorf("isRegistryOrigin(%q, %q) = %v, want %v", tt.u, tt.registry, got, tt.want)
		}
	}
}

// TS normalizeHttpsAuthority lower-cases a bare host[:port] and rejects
// anything else with "Invalid authority '<authority>'."; an empty authority
// has no host.
func TestNormalizeHTTPSAuthority(t *testing.T) {
	if got, err := normalizeHTTPSAuthority("Reg.Example:5000"); err != nil || got != "reg.example:5000" {
		t.Errorf("normalizeHTTPSAuthority(Reg.Example:5000) = %q, %v", got, err)
	}
	for _, authority := range []string{"", "a?q", "a#f", "a b"} {
		if _, err := normalizeHTTPSAuthority(authority); err == nil || err.Error() != "Invalid authority '"+authority+"'." {
			t.Errorf("normalizeHTTPSAuthority(%q) error = %v", authority, err)
		}
	}
}

// Only token requests are held to the no-redirect rule (TS
// requestResolvingRedirects guards the token fetch alone): a blob on the
// registry that redirects to storage, on the host of a known token endpoint,
// passes under hardening.
func TestRegistryRedirectBesideTokenEndpointPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://storage.example/blob", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	policy, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatal(err)
	}
	transport := newAuthPolicyTransport(http.DefaultTransport, policy, "http", strings.TrimPrefix(srv.URL, "http://"))
	transport.addTokenEndpoint(mustURL(t, srv.URL+"/token"))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v2/x/blobs/sha256:abc", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("blob redirect refused: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", resp.StatusCode)
	}
}

// TS records authServerRedirect when follow-redirects followed the token
// request, which it does for a 3xx carrying a Location: 300 counts, a 201 or
// a 400 with a Location header does not.
func TestTokenRedirectStatusRange(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   bool
	}{
		{http.StatusMultipleChoices, true},
		{http.StatusCreated, false},
		{http.StatusBadRequest, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://elsewhere.example/token")
			w.WriteHeader(tt.status)
		}))
		policy, err := NewAuthPolicy(false, nil, log.Null)
		if err != nil {
			t.Fatal(err)
		}
		transport := newAuthPolicyTransport(http.DefaultTransport, policy, "http", strings.TrimPrefix(srv.URL, "http://"))
		transport.addTokenEndpoint(mustURL(t, srv.URL+"/token"))
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/token", nil)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("status %d: %v", tt.status, err)
		}
		resp.Body.Close()
		srv.Close()
		if got := policy.Diagnostics().AuthServerRedirect; got != tt.want {
			t.Errorf("status %d: authServerRedirect = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// TS requestEnsureAuthenticated reads the challenge of a 403 (insufficient
// scope) as it does a 401's.
func TestForbiddenChallengeIsInspected(t *testing.T) {
	policy, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundTrip(t, policy, "http", "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://evil.example/token",service="registry"`)
		w.WriteHeader(http.StatusForbidden)
	}, "/v2/x/manifests/1"); err != nil {
		t.Fatal(err)
	}
	if !policy.Diagnostics().AuthLookupWouldBeBlocked {
		t.Fatal("the 403 challenge's realm was not checked")
	}
}

// TS fails the authentication when `new URL(realm)` throws, as it does for a
// relative realm; Go refuses such a realm under hardening as unparsable.
func TestRelativeRealmIsUnparsableUnderHardening(t *testing.T) {
	policy, err := NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatal(err)
	}
	_, err = roundTrip(t, policy, "http", "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}, "/v2/x/manifests/1")
	if err == nil || !strings.Contains(err.Error(), "requested authentication from an unparsable realm '/token'") {
		t.Fatalf("error = %v, want the unparsable-realm error", err)
	}
}
