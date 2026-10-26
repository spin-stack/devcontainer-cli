package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	godigest "github.com/opencontainers/go-digest"

	"github.com/devcontainers/cli/internal/log"
)

// A registry that accepts the request and never answers must not hold a
// caller that brought no deadline of its own: the client's transport gives up
// waiting for response headers.
func TestClientGivesUpOnRegistryThatNeverAnswers(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-blocked }))
	defer srv.Close()
	defer close(blocked)

	orig := responseHeaderTimeout
	responseHeaderTimeout = 100 * time.Millisecond
	defer func() { responseHeaderTimeout = orig }()

	c := NewClientWithAuthPolicy(log.Null, map[string]string{"DOCKER_CONFIG": t.TempDir()}, DefaultAuthPolicy())
	ref := &Ref{Registry: localhostRegistry(t, srv), Path: "me/feat", Resource: "me/feat"}
	done := make(chan error, 1)
	go func() {
		_, err := c.FetchBlobContext(context.Background(), ref, "sha256:"+strings.Repeat("a", 64))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fetch from a registry that never answers succeeded")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("fetch waited on a registry that never answers")
	}
}

// TS getManifest (containerCollectionsOCI.ts) fails only when the manifest's
// content digest differs from the expected one: fetching a manifest by the
// digest it has succeeds and reports that digest.
func TestFetchManifestByItsOwnDigest(t *testing.T) {
	reg := newFakeOCIRegistry()
	host := reg.start(t)
	body := []byte(`{"schemaVersion":2,"mediaType":"` + OCIManifestMediaType + `","config":{"mediaType":"` + ManifestMediaType + `","digest":"sha256:` + strings.Repeat("0", 64) + `","size":0},"layers":[]}`)
	digest := godigest.FromBytes(body).String()
	reg.manifests[digest] = body

	ref, err := ParseRef(host + "/me/features/feat:1")
	if err != nil {
		t.Fatal(err)
	}
	c := NewClientWithAuthPolicy(log.Null, map[string]string{"DOCKER_CONFIG": t.TempDir()}, DefaultAuthPolicy())
	got, err := c.FetchManifest(ref, digest)
	if err != nil {
		t.Fatalf("FetchManifest by digest: %v", err)
	}
	if got.ContentDigest != digest || got.CanonicalID != ref.Resource+"@"+digest {
		t.Fatalf("content digest %q, canonical id %q", got.ContentDigest, got.CanonicalID)
	}
}
