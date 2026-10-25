// Package httpx provides the proxy- and custom-CA-aware HTTP transport shared by the CLI.
package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/net/http/httpproxy"
)

// NewTransport returns the single *http.Transport used by every HTTP path in the
// CLI (the control manifest, OCI/registry access via oras, and direct tarball
// downloads), so proxy and custom-CA behavior is uniform.
//
// Proxy: it honors the standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment
// variables (and their lowercase forms). Unlike http.ProxyFromEnvironment — which
// caches the environment once per process via sync.Once — this reads the
// environment on each request through golang.org/x/net/http/httpproxy, so proxy
// configuration is picked up reliably (and is deterministically testable).
//
// TLS: extra CA certificates are loaded from NODE_EXTRA_CA_CERTS (TS parity) or
// SSL_CERT_FILE, in addition to the system pool. This matters behind a
// TLS-intercepting corporate proxy, whose CA must be trusted or every HTTPS
// request (registry pulls included) fails — which looks like "the proxy is
// ignored" but is really an untrusted proxy certificate.
func NewTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Resolve the proxy from the environment on every request (fresh read).
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
	}

	// Load extra CA certs (NODE_EXTRA_CA_CERTS for TS parity, SSL_CERT_FILE for Go convention).
	caFile := os.Getenv("NODE_EXTRA_CA_CERTS")
	if caFile == "" {
		caFile = os.Getenv("SSL_CERT_FILE")
	}
	if caFile != "" {
		pool, err := loadCACerts(caFile)
		if err != nil {
			// A bad NODE_EXTRA_CA_CERTS/SSL_CERT_FILE otherwise fails silently and
			// then every HTTPS request behind a TLS-intercepting proxy breaks,
			// looking like "the proxy is ignored". Surface it instead of swallowing.
			fmt.Fprintf(os.Stderr, "warning: could not load CA certs from %s: %v\n", caFile, err)
		} else if pool != nil {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.RootCAs = pool
		}
	}

	return transport
}

func loadCACerts(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(data)
	return pool, nil
}
