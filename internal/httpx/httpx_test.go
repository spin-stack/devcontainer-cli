package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
			resp, err := c.Do(tt.opts(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}
