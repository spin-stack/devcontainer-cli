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

// Proxy: it honors the standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment
// variables (and their lowercase forms). Unlike http.ProxyFromEnvironment — which
// caches the environment once per process via sync.Once — this reads the
// environment on each request through golang.org/x/net/http/httpproxy, so proxy
// configuration is picked up reliably (and is deterministically testable).
func NewTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
	}

	caFile := os.Getenv("NODE_EXTRA_CA_CERTS")
	if caFile == "" {
		caFile = os.Getenv("SSL_CERT_FILE")
	}
	if caFile != "" {
		pool, err := loadCACerts(caFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load CA certs from %s: %v\n", caFile, err)
		} else if pool != nil {
			// The clone carries the HTTP/2 ALPN settings unless HTTP/2 is
			// disabled (GODEBUG=http2client=0), when it has no TLS config at all.
			tlsConfig := &tls.Config{}
			if transport.TLSClientConfig != nil {
				tlsConfig = transport.TLSClientConfig.Clone()
			}
			tlsConfig.RootCAs = pool
			transport.TLSClientConfig = tlsConfig
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
