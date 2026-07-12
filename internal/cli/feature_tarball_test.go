package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsGitHubTarballURI(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/owner/repo/releases/download/v1/feat.tgz": true,
		"https://api.github.com/repos/owner/repo/releases/assets/1":   true,
		"https://raw.githubusercontent.com/owner/repo/main/feat.tgz":  false, // not github.com host
		"https://example.com/feat.tgz":                                false,
		"http://github.com/owner/repo/feat.tgz":                       false, // http, not https
	}
	for u, want := range cases {
		if got := isGitHubTarballURI(u); got != want {
			t.Errorf("isGitHubTarballURI(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestIsPlainHTTPURL(t *testing.T) {
	cases := map[string]bool{
		"http://example.com/feat.tgz":   true,
		"https://example.com/feat.tgz":  false,
		"https://localhost:8080/x.tgz":  true, // localhost even over https
		"http://localhost/x.tgz":        true,
		"https://127.0.0.1:5000/x.tgz":  false, // only the literal "localhost" host is special
	}
	for u, want := range cases {
		if got := isPlainHTTPURL(u); got != want {
			t.Errorf("isPlainHTTPURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestFeatureRequestHeaders(t *testing.T) {
	// Always a devcontainer User-Agent.
	h := featureRequestHeaders("https://example.com/x.tgz", nil, nil)
	if h["User-Agent"] != "devcontainer" {
		t.Errorf("User-Agent = %q, want devcontainer", h["User-Agent"])
	}
	if _, ok := h["Authorization"]; ok {
		t.Error("non-GitHub URL must not get an Authorization header")
	}

	// GitHub URL + token → bearer auth.
	h = featureRequestHeaders("https://github.com/o/r/releases/download/v1/f.tgz",
		map[string]string{"GITHUB_TOKEN": "secret"}, nil)
	if h["Authorization"] != "Bearer secret" {
		t.Errorf("Authorization = %q, want 'Bearer secret'", h["Authorization"])
	}

	// GitHub URL without a token → no auth header (not an empty bearer).
	h = featureRequestHeaders("https://github.com/o/r/f.tgz", map[string]string{}, nil)
	if _, ok := h["Authorization"]; ok {
		t.Error("GitHub URL without GITHUB_TOKEN must not set Authorization")
	}

	// Non-GitHub URL never leaks the token.
	h = featureRequestHeaders("https://example.com/f.tgz",
		map[string]string{"GITHUB_TOKEN": "secret"}, nil)
	if _, ok := h["Authorization"]; ok {
		t.Error("non-GitHub URL must not receive the GITHUB_TOKEN")
	}
}

// TestDownloadFeatureTarball_HeadersAndDigest exercises the real HTTP path
// against a local server: it must send the devcontainer UA + bearer token and
// return the correct sha256 digest.
func TestDownloadFeatureTarball_HeadersAndDigest(t *testing.T) {
	payload := []byte("fake-tarball-bytes")
	var gotUA, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	// The test server is not a github.com host, so no auth is expected here; use
	// the header helper test above for the GitHub-auth branch. Verify UA + digest.
	data, digest, err := downloadFeatureTarball(context.Background(), nil, srv.URL, map[string]string{"GITHUB_TOKEN": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Errorf("body = %q, want %q", data, payload)
	}
	if gotUA != "devcontainer" {
		t.Errorf("server saw User-Agent %q, want devcontainer", gotUA)
	}
	if gotAuth != "" {
		t.Errorf("non-GitHub host must not receive Authorization, got %q", gotAuth)
	}
	sum := sha256.Sum256(payload)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if digest != want {
		t.Errorf("digest = %q, want %q", digest, want)
	}
}

// TestDownloadFeatureTarball_Non2xx verifies non-2xx responses surface an error.
func TestDownloadFeatureTarball_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := downloadFeatureTarball(context.Background(), nil, srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("want an HTTP 404 error, got %v", err)
	}
}

// TestDownloadFeatureTarball_2xxAccepted verifies a 2xx that is not 200 (e.g. 206)
// is accepted, matching the TS 200-299 range.
func TestDownloadFeatureTarball_2xxAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204, still 2xx
	}))
	defer srv.Close()

	if _, _, err := downloadFeatureTarball(context.Background(), nil, srv.URL, nil); err != nil {
		t.Errorf("204 should be accepted as success, got %v", err)
	}
}
