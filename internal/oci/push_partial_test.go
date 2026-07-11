package oci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// writeTarball drops a throwaway artifact layer file and returns its path.
func writeTarball(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact.tgz")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPushArtifact_BearerAuthAndPartialTagFailure drives two risks at once
// against a hermetic httptest registry:
//
//   - Auth: the registry requires Bearer auth, so PushArtifact must run the full
//     401 -> WWW-Authenticate: Bearer -> token endpoint -> retry loop (this path
//     is NOT covered by the Basic-auth registry:3 test).
//   - Partial publish / no rollback: the second tag ("latest") fails mid-loop.
//     PushArtifact publishes "1.0.0", logs a warning for "latest", and returns
//     SUCCESS with only the tags that made it. The earlier tag is left published
//     with no rollback -- the caller asked for {1.0.0, latest} and silently got
//     only {1.0.0}. This asserts that exact behavior so a future change to it is
//     a conscious one.
func TestPushArtifact_BearerAuthAndPartialTagFailure(t *testing.T) {
	const user, pass = "u", "p"
	reg := newFakeOCIRegistry()
	reg.requireBearer = true
	reg.wantUser, reg.wantPass = user, pass
	reg.failTags = map[string]bool{"latest": true}
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, dockerConfigEnv(t, registry, user, pass))

	res, err := client.PushArtifact(ref, writeTarball(t, "layer"), []string{"1.0.0", "latest"}, "feature", nil)
	if err != nil {
		t.Fatalf("push returned error, want partial success: %v", err)
	}

	// Only the surviving tag is reported, and it is genuinely in the registry.
	if got := res.PublishedTags; len(got) != 1 || got[0] != "1.0.0" {
		t.Fatalf("PublishedTags = %v, want [1.0.0] (partial, no rollback)", got)
	}
	reg.mu.Lock()
	_, has100 := reg.manifests["1.0.0"]
	_, hasLatest := reg.manifests["latest"]
	tokenReqs := reg.tokenRequests
	reg.mu.Unlock()
	if !has100 {
		t.Error("1.0.0 tag was not published")
	}
	if hasLatest {
		t.Error("latest tag was published despite the injected failure")
	}
	// The bearer loop must actually have run (token endpoint hit at least once).
	if tokenReqs == 0 {
		t.Error("token endpoint never called; the Bearer auth loop did not run")
	}
}

// TestPushArtifact_AllTagsFail asserts the other end of the tag loop: when every
// tag PUT fails, PushArtifact publishes nothing and surfaces an error rather than
// a bogus empty-tag success.
func TestPushArtifact_AllTagsFail(t *testing.T) {
	reg := newFakeOCIRegistry()
	reg.failAllManifests = true
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, nil)

	if _, err := client.PushArtifact(ref, writeTarball(t, "layer"), []string{"1", "1.0.0", "latest"}, "feature", nil); err == nil {
		t.Fatal("expected an error when all tags fail, got nil")
	}
}

// TestPushArtifact_BlobExistsSkipsReupload covers pushBlobIfAbsent's
// already-present branch: on a second publish the layer/config blobs already
// exist, so their HEAD returns 200 and the blob is not re-uploaded. This guards
// the idempotency of the publish path.
func TestPushArtifact_BlobExistsSkipsReupload(t *testing.T) {
	reg := newFakeOCIRegistry()
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, nil)
	tgz := writeTarball(t, "same-layer-bytes")

	if _, err := client.PushArtifact(ref, tgz, []string{"1.0.0"}, "feature", nil); err != nil {
		t.Fatalf("first push failed: %v", err)
	}
	reg.mu.Lock()
	uploadsAfterFirst := reg.blobUploads
	reg.mu.Unlock()
	if uploadsAfterFirst == 0 {
		t.Fatal("first push uploaded no blobs")
	}

	// Re-publish the identical artifact under a new tag: blobs are unchanged.
	ref2, _ := ParseRef(registry + "/ns/hello:1.0.1")
	if _, err := client.PushArtifact(ref2, tgz, []string{"1.0.1"}, "feature", nil); err != nil {
		t.Fatalf("second push failed: %v", err)
	}
	reg.mu.Lock()
	uploadsAfterSecond := reg.blobUploads
	headHits := reg.blobHeadHits
	reg.mu.Unlock()

	if uploadsAfterSecond != uploadsAfterFirst {
		t.Errorf("second push re-uploaded blobs (%d -> %d); pushBlobIfAbsent did not skip existing blobs", uploadsAfterFirst, uploadsAfterSecond)
	}
	if headHits == 0 {
		t.Error("no blob HEAD hit found an existing blob; existence check never short-circuited")
	}
}

// TestPushCollectionMetadata_SuccessAndFailure covers the separate
// collection-metadata push (0% covered before): the happy path tags the
// collection artifact `latest`, and a registry that rejects the manifest surfaces
// the error -- this is the push that publishCollectionWith counts as a failure.
func TestPushCollectionMetadata_SuccessAndFailure(t *testing.T) {
	collPath := filepath.Join(t.TempDir(), "devcontainer-collection.json")
	if err := os.WriteFile(collPath, []byte(`{"features":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("success", func(t *testing.T) {
		reg := newFakeOCIRegistry()
		registry := reg.start(t)
		ref, err := ParseRef(registry + "/me/features")
		if err != nil {
			t.Fatal(err)
		}
		client := NewClient(log.Null, nil)
		res, err := client.PushCollectionMetadata(ref, collPath)
		if err != nil {
			t.Fatalf("collection metadata push failed: %v", err)
		}
		if len(res.PublishedTags) != 1 || res.PublishedTags[0] != "latest" {
			t.Fatalf("PublishedTags = %v, want [latest]", res.PublishedTags)
		}
		reg.mu.Lock()
		_, ok := reg.manifests["latest"]
		reg.mu.Unlock()
		if !ok {
			t.Error("collection manifest not tagged latest in registry")
		}
	})

	t.Run("manifest push rejected", func(t *testing.T) {
		reg := newFakeOCIRegistry()
		reg.failAllManifests = true
		registry := reg.start(t)
		ref, err := ParseRef(registry + "/me/features")
		if err != nil {
			t.Fatal(err)
		}
		client := NewClient(log.Null, nil)
		if _, err := client.PushCollectionMetadata(ref, collPath); err == nil {
			t.Fatal("expected error when the collection manifest is rejected, got nil")
		}
	})

	t.Run("missing metadata file", func(t *testing.T) {
		reg := newFakeOCIRegistry()
		registry := reg.start(t)
		ref, _ := ParseRef(registry + "/me/features")
		client := NewClient(log.Null, nil)
		if _, err := client.PushCollectionMetadata(ref, filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("expected error for a missing metadata file, got nil")
		}
	})
}
