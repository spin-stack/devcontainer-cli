package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	tests := []struct {
		name    string
		version string
		handler func(t *testing.T) http.HandlerFunc
		opts    func(url string) RequestOptions
		check   func(t *testing.T, resp *Response)
	}{
		{
			name:    "GET",
			version: "test",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Errorf("method = %s", r.Method)
					}
					if ua := r.Header.Get("User-Agent"); ua == "" {
						t.Error("missing User-Agent")
					}
					w.Header().Set("X-Custom", "test")
					w.WriteHeader(200)
					w.Write([]byte(`{"ok": true}`))
				}
			},
			opts: func(url string) RequestOptions {
				return RequestOptions{URL: url}
			},
			check: func(t *testing.T, resp *Response) {
				if resp.StatusCode != 200 {
					t.Errorf("status = %d", resp.StatusCode)
				}
				if resp.Headers.Get("X-Custom") != "test" {
					t.Error("missing custom header")
				}
				if string(resp.Body) != `{"ok": true}` {
					t.Errorf("body = %q", string(resp.Body))
				}
			},
		},
		{
			name:    "POST",
			version: "test",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "POST" {
						t.Errorf("method = %s", r.Method)
					}
					if ct := r.Header.Get("Content-Type"); ct != "application/json" {
						t.Errorf("content-type = %s", ct)
					}
					w.WriteHeader(201)
				}
			},
			opts: func(url string) RequestOptions {
				return RequestOptions{
					Method: "POST",
					URL:    url,
					Headers: map[string]string{
						"Content-Type": "application/json",
					},
				}
			},
			check: func(t *testing.T, resp *Response) {
				if resp.StatusCode != 201 {
					t.Errorf("status = %d", resp.StatusCode)
				}
			},
		},
		{
			name:    "CustomHeaders",
			version: "test",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if auth := r.Header.Get("Authorization"); auth != "Bearer token123" {
						t.Errorf("authorization = %q", auth)
					}
					w.WriteHeader(200)
				}
			},
			opts: func(url string) RequestOptions {
				return RequestOptions{
					URL: url,
					Headers: map[string]string{
						"Authorization": "Bearer token123",
					},
				}
			},
			check: func(t *testing.T, resp *Response) {
				if resp.StatusCode != 200 {
					t.Errorf("status = %d", resp.StatusCode)
				}
			},
		},
		{
			name:    "UserAgent",
			version: "1.2.3",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if ua := r.Header.Get("User-Agent"); ua != "devcontainer-cli/1.2.3" {
						t.Errorf("user-agent = %q", ua)
					}
					w.WriteHeader(200)
				}
			},
			opts: func(url string) RequestOptions {
				return RequestOptions{URL: url}
			},
			check: nil,
		},
		{
			name:    "Redirect",
			version: "test",
			handler: func(t *testing.T) http.HandlerFunc {
				final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("final"))
				}))
				t.Cleanup(final.Close)
				return func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, final.URL, http.StatusFound)
				}
			},
			opts: func(url string) RequestOptions {
				return RequestOptions{URL: url}
			},
			check: func(t *testing.T, resp *Response) {
				if string(resp.Body) != "final" {
					t.Errorf("body = %q", string(resp.Body))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler(t))
			defer srv.Close()

			c := New(tt.version)
			resp, err := c.Do(t.Context(), tt.opts(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}

// TestDoRedirectChain follows a multi-hop redirect chain end to end and confirms
// every hop was visited and the final body is returned.
func TestDoRedirectChain(t *testing.T) {
	var hops int32
	mux := http.NewServeMux()
	// /a -> /b -> /c -> final body
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hops, 1)
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hops, 1)
		http.Redirect(w, r, "/c", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hops, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("arrived"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("test")
	resp, err := c.Do(t.Context(), RequestOptions{URL: srv.URL + "/a"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != "arrived" {
		t.Errorf("body = %q, want %q", string(resp.Body), "arrived")
	}
	if got := atomic.LoadInt32(&hops); got != 3 {
		t.Errorf("visited %d hops, want 3 (full chain followed)", got)
	}
}

// TestDoCheckRedirectPolicy verifies SetCheckRedirect can stop the CLI from
// following redirects (returning the 302 itself).
func TestDoCheckRedirectPolicy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/from", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/to", http.StatusFound)
	})
	mux.HandleFunc("/to", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should-not-reach"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("test")
	c.SetCheckRedirect(func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	})
	resp, err := c.Do(t.Context(), RequestOptions{URL: srv.URL + "/from"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 (redirect not followed)", resp.StatusCode)
	}
	if string(resp.Body) == "should-not-reach" {
		t.Error("redirect was followed despite ErrUseLastResponse policy")
	}
}

// TestDoContextCancelled confirms a cancelled context aborts the request rather
// than blocking on a slow/hung server.
func TestDoContextCancelled(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // block until the test releases us
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()

	c := New("test")
	_, err := c.Do(ctx, RequestOptions{URL: srv.URL})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestDoContextDeadline confirms a context deadline aborts a slow request.
func TestDoContextDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	c := New("test")
	_, err := c.Do(ctx, RequestOptions{URL: srv.URL})
	if err == nil {
		t.Fatal("expected error from context deadline, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}
