package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
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

// New creates a Client configured with proxy env vars and optional extra CA certs.
// It reads NODE_EXTRA_CA_CERTS and SSL_CERT_FILE for TLS CA bundles.
func New(version string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Load extra CA certs (NODE_EXTRA_CA_CERTS for TS parity, SSL_CERT_FILE for Go convention)
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

	// Proxy is handled automatically by http.Transport using HTTP_PROXY/HTTPS_PROXY/NO_PROXY env vars.

	return &Client{
		inner: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		version: version,
	}
}

// Do executes an HTTP request and returns the response.
func (c *Client) Do(opts RequestOptions) (*Response, error) {
	if opts.Method == "" {
		opts.Method = "GET"
	}

	req, err := http.NewRequest(opts.Method, opts.URL, opts.Body)
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
