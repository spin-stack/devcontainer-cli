package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Error("missing User-Agent")
		}
		w.Header().Set("X-Custom", "test")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	c := New("test")
	resp, err := c.Do(RequestOptions{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.Headers.Get("X-Custom") != "test" {
		t.Error("missing custom header")
	}
	if string(resp.Body) != `{"ok": true}` {
		t.Errorf("body = %q", string(resp.Body))
	}
}

func TestClient_POST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s", ct)
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()

	c := New("test")
	resp, err := c.Do(RequestOptions{
		Method: "POST",
		URL:    srv.URL,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestClient_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer token123" {
			t.Errorf("authorization = %q", auth)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New("test")
	resp, err := c.Do(RequestOptions{
		URL: srv.URL,
		Headers: map[string]string{
			"Authorization": "Bearer token123",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestClient_UserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "devcontainer-cli/1.2.3" {
			t.Errorf("user-agent = %q", ua)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New("1.2.3")
	_, err := c.Do(RequestOptions{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClient_Redirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("final"))
	}))
	defer final.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirect.Close()

	c := New("test")
	resp, err := c.Do(RequestOptions{URL: redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "final" {
		t.Errorf("body = %q", string(resp.Body))
	}
}
