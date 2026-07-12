package oci

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/devcontainers/cli/internal/log"
)

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"errdef.ErrNotFound", errdef.ErrNotFound, true},
		{"wrapped ErrNotFound", fmt.Errorf("listing: %w", errdef.ErrNotFound), true},
		{"404 ErrorResponse", &errcode.ErrorResponse{StatusCode: http.StatusNotFound}, true},
		{"wrapped 404", fmt.Errorf("tags: %w", &errcode.ErrorResponse{StatusCode: 404}), true},
		{"401 ErrorResponse", &errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}, false},
		{"500 ErrorResponse", &errcode.ErrorResponse{StatusCode: http.StatusInternalServerError}, false},
		{"plain error", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestGetPublishedTags_HangingRegistryTimesOut verifies the fix for the
// indefinite-block finding: a registry that accepts the connection but never
// sends response headers must not hang the operation forever — the default
// per-operation deadline (shrunk here) cuts it off.
func TestGetPublishedTags_HangingRegistryTimesOut(t *testing.T) {
	// A server that accepts the request and then never responds.
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked // hang until the test ends
	}))
	defer srv.Close()
	defer close(blocked)

	// Shrink the metadata deadline so the test is fast.
	orig := metadataOpTimeout
	metadataOpTimeout = 200 * time.Millisecond
	defer func() { metadataOpTimeout = orig }()

	c := NewClient(log.New(log.Options{Level: log.LevelError}), nil)
	ref := &Ref{Registry: strings.TrimPrefix(srv.URL, "http://"), Path: "me/feat", Resource: "me/feat"}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := c.GetPublishedTags(ref)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("operation took %s, expected it to time out promptly", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetPublishedTags did not honor the deadline — it blocked indefinitely")
	}
}
