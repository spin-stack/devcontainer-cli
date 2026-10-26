package oci

import (
	"net"
	"net/http/httptest"
	"testing"
)

// localhostRegistry is srv's authority as a localhost registry host. httptest
// listens on 127.0.0.1, which the reference and Ref.Scheme address over HTTPS;
// only a localhost registry is plain HTTP.
func localhostRegistry(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return "localhost:" + port
}
