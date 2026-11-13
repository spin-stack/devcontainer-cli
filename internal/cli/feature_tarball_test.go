package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
)

func TestIsGitHubTarballURI(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/owner/repo/releases/download/v1/feat.tgz":    true,
		"https://api.github.com/repos/owner/repo/releases/assets/1":      true,
		"https://raw.githubusercontent.com/owner/repo/main/feat.tgz":     false,
		"https://example.com/feat.tgz":                                   false,
		"http://github.com/owner/repo/feat.tgz":                          false,
		"https://GitHub.COM/owner/repo/feat.tgz":                         true,
		"HTTPS://API.github.com/repos/owner/repo/releases/assets/1":      true,
		"https://github.com./owner/repo/feat.tgz":                        true,
		"https://github.com:443/owner/repo/feat.tgz":                     true,
		"https://github.com.attacker.example/devcontainer-feature-x.tgz": false,
		"https://github.com@attacker.example/devcontainer-feature-x.tgz": false,
		"https://github.com:pw@attacker.example/feat.tgz":                false,
		"https://user@github.com/owner/repo/feat.tgz":                    false,
		"https://api.github.com.evil/devcontainer-feature-x.tgz":         false,
		"https://github.company.example/feat.tgz":                        false,
		"https://evilgithub.com/feat.tgz":                                false,
		"http://api.github.com/repos/owner/repo/releases/assets/1":       false,
		"https://github.com\\@attacker.example/feat.tgz":                 false,
		"github.com/owner/repo/feat.tgz":                                 false,
	}
	for u, want := range cases {
		if got := isGitHubTarballURI(u); got != want {
			t.Errorf("isGitHubTarballURI(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestIsPlainHTTPURL(t *testing.T) {
	cases := map[string]bool{
		"http://example.com/feat.tgz":  true,
		"https://example.com/feat.tgz": false,
		"https://localhost:8080/x.tgz": true,
		"http://localhost/x.tgz":       true,
		"https://127.0.0.1:5000/x.tgz": false,
	}
	for u, want := range cases {
		if got := isPlainHTTPURL(u); got != want {
			t.Errorf("isPlainHTTPURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestFeatureRequestHeaders(t *testing.T) {
	h := featureRequestHeaders("https://example.com/x.tgz", nil, nil)
	if h["User-Agent"] != "devcontainer" {
		t.Errorf("User-Agent = %q, want devcontainer", h["User-Agent"])
	}
	if _, ok := h["Authorization"]; ok {
		t.Error("non-GitHub URL must not get an Authorization header")
	}

	h = featureRequestHeaders("https://github.com/o/r/releases/download/v1/f.tgz",
		map[string]string{"GITHUB_TOKEN": "secret"}, nil)
	if h["Authorization"] != "Bearer secret" {
		t.Errorf("Authorization = %q, want 'Bearer secret'", h["Authorization"])
	}

	h = featureRequestHeaders("https://github.com/o/r/f.tgz", map[string]string{}, nil)
	if _, ok := h["Authorization"]; ok {
		t.Error("GitHub URL without GITHUB_TOKEN must not set Authorization")
	}

	h = featureRequestHeaders("https://example.com/f.tgz",
		map[string]string{"GITHUB_TOKEN": "secret"}, nil)
	if _, ok := h["Authorization"]; ok {
		t.Error("non-GitHub URL must not receive the GITHUB_TOKEN")
	}
}

// GITHUB_TOKEN goes only to GitHub's own hosts. TS sends it to every URI that
// starts with "https://github.com" or "https://api.github.com", look-alike
// hosts included (docs/DIVERGENCES.md); no parity case can compare that
// without leaking a token, so these cases pin it.
func TestFeatureRequestHeadersSendTheTokenOnlyToGitHub(t *testing.T) {
	cases := []struct {
		url    string
		github bool
	}{
		{"https://github.com/o/r/releases/download/v1/devcontainer-feature-x.tgz", true},
		{"https://api.github.com/repos/o/r/releases/assets/1", true},
		{"https://GitHub.com/o/r/devcontainer-feature-x.tgz", true},
		{"https://github.com.attacker.example/devcontainer-feature-x.tgz", false},
		{"https://github.com@attacker.example/devcontainer-feature-x.tgz", false},
		{"https://api.github.com.evil/devcontainer-feature-x.tgz", false},
		{"http://github.com/o/r/devcontainer-feature-x.tgz", false},
	}
	for _, c := range cases {
		for _, token := range []string{"secret", ""} {
			var logged bytes.Buffer
			logger := log.New(log.Options{Writer: &logged, Format: "json"})
			h := featureRequestHeaders(c.url, map[string]string{"GITHUB_TOKEN": token}, logger)

			wantAuth, wantLog := "", ""
			if c.github && token != "" {
				wantAuth, wantLog = "Bearer secret", "Using environment GITHUB_TOKEN."
			} else if c.github {
				wantLog = "No environment GITHUB_TOKEN available."
			}
			if got := h["Authorization"]; got != wantAuth {
				t.Errorf("%s (token %q): Authorization = %q, want %q", c.url, token, got, wantAuth)
			}
			if h["User-Agent"] != "devcontainer" {
				t.Errorf("%s: User-Agent = %q, want devcontainer", c.url, h["User-Agent"])
			}
			if wantLog != "" && !strings.Contains(logged.String(), wantLog) {
				t.Errorf("%s (token %q): log %q lacks %q", c.url, token, logged.String(), wantLog)
			}
			if wantLog == "" && strings.Contains(logged.String(), "GITHUB_TOKEN") {
				t.Errorf("%s (token %q): log mentions GITHUB_TOKEN: %q", c.url, token, logged.String())
			}
		}
	}
}

// A URL whose userinfo is "github.com" reaches another server; that server
// must not see the token.
func TestDownloadFeatureTarballKeepsTheTokenFromALookAlikeURL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte("tgz"))
	}))
	defer srv.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	t.Setenv("SSL_CERT_FILE", caFile)

	lookAlike := "https://github.com@" + strings.TrimPrefix(srv.URL, "https://") + "/devcontainer-feature-x.tgz"
	if _, _, err := downloadFeatureTarball(t.Context(), nil, lookAlike, map[string]string{"GITHUB_TOKEN": "secret"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotAuth, "Bearer") || strings.Contains(gotAuth, "secret") {
		t.Errorf("look-alike host received the token: Authorization = %q", gotAuth)
	}
}

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

func TestDownloadFeatureTarball_2xxAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if _, _, err := downloadFeatureTarball(context.Background(), nil, srv.URL, nil); err != nil {
		t.Errorf("204 should be accepted as success, got %v", err)
	}
}

func TestDirectTarballDigestCheckedAgainstTheLockfile(t *testing.T) {
	uri := serveTrustedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-a-real-tarball"))
	})) + "/feature.tgz"
	sum := sha256.Sum256([]byte("not-a-real-tarball"))
	digest := "sha256:" + hex.EncodeToString(sum[:])

	resolve := func(lf *features.Lockfile) error {
		_, err := newMetadataProcessFeature(nil, log.Null, localFeatureRoots{}, lf)(&features.FNode{UserFeatureID: uri})
		return err
	}
	wrong := &features.Lockfile{Features: map[string]features.LockfileEntry{uri: {Integrity: "sha256:" + strings.Repeat("0", 64)}}}
	if err := resolve(wrong); err == nil || err.Error() != "Digest did not match for "+uri+"." {
		t.Errorf("wrong integrity: err = %v, want %q", err, "Digest did not match for "+uri+".")
	}
	right := &features.Lockfile{Features: map[string]features.LockfileEntry{uri: {Integrity: digest}}}
	for name, lf := range map[string]*features.Lockfile{"no lockfile": nil, "matching integrity": right} {
		if err := resolve(lf); err != nil && strings.Contains(err.Error(), "Digest did not match") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := verifyTarballDigest(wrong, uri, digest); err == nil || err.Error() != "Digest did not match for "+uri+"." {
		t.Errorf("install path: err = %v", err)
	}
}
