package oci

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	godigest "github.com/opencontainers/go-digest"
)

// fakeOCIRegistry is a minimal in-memory OCI distribution registry served over
// httptest. It speaks just enough of the pull/push protocol to drive oras-go
// (blob HEAD/upload, manifest PUT, tags list) and, crucially, lets a test:
//
//   - require Bearer auth so the full 401 -> WWW-Authenticate: Bearer -> token
//     endpoint -> retry loop runs against a real HTTP server (the registry:3
//     testcontainer only exercises Basic auth); and
//   - inject a mid-loop manifest failure (failTags) to reproduce the
//     no-rollback partial-publish risk in PushArtifact's tag loop.
//
// It runs on 127.0.0.1 (httptest), so isLocalRegistry() flips PlainHTTP on and
// no TLS is needed.
type fakeOCIRegistry struct {
	mu     sync.Mutex
	srvURL string // set by start(); used to build the Bearer realm URL

	// blobs indexed by digest; manifests indexed by tag/reference.
	blobs     map[string][]byte
	manifests map[string][]byte

	// requireBearer turns on the token-auth challenge. wantUser/wantPass are the
	// Basic credentials the /token endpoint accepts.
	requireBearer      bool
	wantUser, wantPass string
	issuedToken        string

	// failTags: manifest PUT for any of these references returns 500, simulating a
	// partial failure part-way through the publish loop. failAllManifests fails
	// every manifest PUT.
	failTags         map[string]bool
	failAllManifests bool

	// Observed activity, for assertions.
	tokenRequests int
	putManifestOK []string // references successfully stored
	blobUploads   int
	blobHeadHits  int // HEAD requests that found an existing blob
}

func newFakeOCIRegistry() *fakeOCIRegistry {
	return &fakeOCIRegistry{
		blobs:       map[string][]byte{},
		manifests:   map[string][]byte{},
		failTags:    map[string]bool{},
		issuedToken: "issued-bearer-token",
	}
}

func (r *fakeOCIRegistry) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(srv.Close)
	r.srvURL = srv.URL
	// Strip the scheme; the registry host is what ParseRef expects.
	return strings.TrimPrefix(srv.URL, "http://")
}

func (r *fakeOCIRegistry) authorized(req *http.Request) bool {
	if !r.requireBearer {
		return true
	}
	return req.Header.Get("Authorization") == "Bearer "+r.issuedToken
}

func (r *fakeOCIRegistry) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate",
		`Bearer realm="`+r.srvURL+`/token",service="fake-registry",scope="repository:*:pull,push"`)
	w.WriteHeader(http.StatusUnauthorized)
}

func (r *fakeOCIRegistry) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := req.URL.Path

	// Token endpoint: validate Basic creds, issue a bearer token.
	if path == "/token" {
		r.tokenRequests++
		user, pass, ok := req.BasicAuth()
		if !ok || user != r.wantUser || pass != r.wantPass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": r.issuedToken, "access_token": r.issuedToken})
		return
	}

	// The API-version probe and every data path require auth when Bearer is on.
	if !r.authorized(req) {
		r.challenge(w)
		return
	}

	if path == "/v2/" || path == "/v2" {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch {
	case strings.HasSuffix(path, "/tags/list"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/v2/"), "/tags/list")
		tags := make([]string, 0, len(r.manifests))
		for ref := range r.manifests {
			tags = append(tags, ref)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "tags": tags})

	case strings.Contains(path, "/blobs/uploads"):
		r.handleBlobUpload(w, req)

	case strings.Contains(path, "/blobs/"):
		r.handleBlob(w, req, path)

	case strings.Contains(path, "/manifests/"):
		r.handleManifest(w, req, path)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (r *fakeOCIRegistry) handleBlobUpload(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
		// Start an upload session; oras will PUT the content next.
		w.Header().Set("Location", req.URL.Path+"session-id")
		w.Header().Set("Docker-Upload-UUID", "session-id")
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		digest := req.URL.Query().Get("digest")
		body, _ := io.ReadAll(req.Body)
		if digest != "" {
			r.blobs[digest] = body
			r.blobUploads++
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *fakeOCIRegistry) handleBlob(w http.ResponseWriter, req *http.Request, path string) {
	i := strings.LastIndex(path, "/blobs/")
	digest := path[i+len("/blobs/"):]
	data, ok := r.blobs[digest]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if req.Method == http.MethodHead {
		r.blobHeadHits++
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (r *fakeOCIRegistry) handleManifest(w http.ResponseWriter, req *http.Request, path string) {
	i := strings.LastIndex(path, "/manifests/")
	ref := path[i+len("/manifests/"):]

	switch req.Method {
	case http.MethodPut:
		if r.failAllManifests || r.failTags[ref] {
			// 400 is a non-retryable client error, so oras returns immediately
			// (a 5xx would trigger retry backoff and slow the test), while still
			// exercising the tag-loop failure branch in PushArtifact.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(req.Body)
		r.manifests[ref] = body
		r.putManifestOK = append(r.putManifestOK, ref)
		w.Header().Set("Docker-Content-Digest", godigest.FromBytes(body).String())
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet, http.MethodHead:
		body, ok := r.manifests[ref]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", godigest.FromBytes(body).String())
		w.Header().Set("Content-Type", OCIManifestMediaType)
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
