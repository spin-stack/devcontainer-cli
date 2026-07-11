package httpx

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// clearProxyEnv sets every proxy-related variable (upper and lower case) to a
// controlled value so the test is not perturbed by the developer's environment.
// httpproxy.FromEnvironment reads these fresh on every request, so t.Setenv makes
// the proxy behavior deterministic.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
		"NO_PROXY", "no_proxy", "ALL_PROXY", "all_proxy",
	} {
		t.Setenv(k, "")
	}
}

func TestNewTransportProxySelection(t *testing.T) {
	tr := NewTransport()
	if tr.Proxy == nil {
		t.Fatal("NewTransport: Proxy must be set so HTTP(S)_PROXY/NO_PROXY are honored")
	}

	tests := []struct {
		name      string
		httpProxy string
		noProxy   string
		target    string
		wantProxy string // "" means no proxy (direct)
	}{
		{name: "http proxy used", httpProxy: "http://proxy.local:3128", target: "http://registry.example.com/v2/", wantProxy: "http://proxy.local:3128"},
		{name: "no_proxy excludes host", httpProxy: "http://proxy.local:3128", noProxy: "example.com", target: "http://registry.example.com/v2/", wantProxy: ""},
		{name: "no proxy configured", target: "http://registry.example.com/v2/", wantProxy: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearProxyEnv(t)
			if tc.httpProxy != "" {
				t.Setenv("HTTP_PROXY", tc.httpProxy)
			}
			if tc.noProxy != "" {
				t.Setenv("NO_PROXY", tc.noProxy)
			}

			req, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tr.Proxy(req)
			if err != nil {
				t.Fatalf("Proxy(): %v", err)
			}
			if tc.wantProxy == "" {
				if got != nil {
					t.Fatalf("expected direct (no proxy), got %s", got)
				}
				return
			}
			if got == nil || got.String() != tc.wantProxy {
				t.Fatalf("expected proxy %s, got %v", tc.wantProxy, got)
			}
		})
	}
}

// TestNewTransportRoutesThroughProxy proves the transport actually dials the
// proxy for a real request: an httptest server stands in for the proxy, and a
// plain-HTTP request to a non-loopback host must arrive at it.
func TestNewTransportRoutesThroughProxy(t *testing.T) {
	clearProxyEnv(t)

	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)

	client := &http.Client{Transport: NewTransport()}
	// registry.internal never resolves; if the proxy is honored the request goes
	// to the httptest proxy instead and never touches DNS.
	resp, err := client.Get("http://registry.internal/v2/")
	if err != nil {
		t.Fatalf("request did not go through the proxy: %v", err)
	}
	resp.Body.Close()

	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected the proxy to receive exactly 1 request, got %d", hits)
	}
}

// TestNewTransportTrustsExtraCA proves NODE_EXTRA_CA_CERTS is honored end to end:
// an HTTPS server with a self-signed cert is only reachable when its CA is loaded
// into the transport — the exact failure mode behind a TLS-intercepting proxy.
func TestNewTransportTrustsExtraCA(t *testing.T) {
	clearProxyEnv(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Without the extra CA, the self-signed server cert is untrusted.
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	t.Setenv("SSL_CERT_FILE", "")
	if _, err := (&http.Client{Transport: NewTransport()}).Get(srv.URL); err == nil {
		t.Fatal("expected TLS failure without the extra CA")
	}

	// Write the server's cert as a PEM bundle and point NODE_EXTRA_CA_CERTS at it.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_EXTRA_CA_CERTS", caFile)

	resp, err := (&http.Client{Transport: NewTransport()}).Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success once the CA is trusted, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// Guard: the transport must never disable TLS verification.
func TestNewTransportDoesNotSkipTLSVerify(t *testing.T) {
	tr := NewTransport()
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("NewTransport must not set InsecureSkipVerify")
	}
	// Sanity: a fresh transport with no extra CA env has no custom RootCAs.
	clearProxyEnv(t)
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	t.Setenv("SSL_CERT_FILE", "")
	if base := NewTransport(); base.TLSClientConfig != nil && base.TLSClientConfig.RootCAs != nil {
		t.Fatal("expected no custom RootCAs without NODE_EXTRA_CA_CERTS/SSL_CERT_FILE")
	}
}
