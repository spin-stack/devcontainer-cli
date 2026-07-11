package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Client wraps http.Client with proxy-awareness and custom CA cert support.
type Client struct {
	inner   *http.Client
	version string
}

// RequestOptions describes an HTTP request.
type RequestOptions struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    io.Reader
}

// Response captures the HTTP response.
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// NewTransport returns the single *http.Transport used by every HTTP path in the
// CLI (plain httpx requests, OCI/registry access via oras, and direct tarball
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
		if pool, err := loadCACerts(caFile); err == nil && pool != nil {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.RootCAs = pool
		}
	}

	return transport
}

// New creates a Client configured with proxy env vars and optional extra CA certs.
// It reads NODE_EXTRA_CA_CERTS and SSL_CERT_FILE for TLS CA bundles.
func New(version string) *Client {
	return &Client{
		inner: &http.Client{
			Transport: NewTransport(),
			Timeout:   30 * time.Second,
		},
		version: version,
	}
}

// SetCheckRedirect installs a redirect policy on the underlying http.Client. The
// policy receives the pending request and the chain of requests already made, and
// returning http.ErrUseLastResponse stops following redirects (returning the last
// response). When left unset the Go default is used (follow up to 10 redirects).
func (c *Client) SetCheckRedirect(policy func(req *http.Request, via []*http.Request) error) {
	c.inner.CheckRedirect = policy
}

// Do executes an HTTP request bound to ctx and returns the response. A cancelled
// or deadline-exceeded ctx aborts the request. Redirects are followed per the
// client's CheckRedirect policy (the Go default unless SetCheckRedirect was used).
func (c *Client) Do(ctx context.Context, opts RequestOptions) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Method == "" {
		opts.Method = "GET"
	}

	req, err := http.NewRequestWithContext(ctx, opts.Method, opts.URL, opts.Body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", fmt.Sprintf("devcontainer-cli/%s", c.version))
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.inner.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", opts.Method, opts.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
	}, nil
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
