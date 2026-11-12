package oci

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func writeTarball(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact.tgz")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPushArtifact_BearerAuthAndStopsAtFirstFailedTag(t *testing.T) {
	const user, pass = "u", "p"
	reg := newFakeOCIRegistry()
	reg.requireBearer = true
	reg.wantUser, reg.wantPass = user, pass
	reg.failTags = map[string]bool{"1.0": true}
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	client := NewClient(log.New(log.Options{Writer: &logs, Level: log.LevelInfo}), dockerConfigEnv(t, registry, user, pass))

	res, err := client.PushArtifact(t.Context(), ref, writeTarball(t, "layer"), []string{"1", "1.0", "1.0.0", "latest"}, "feature", nil)
	if err == nil {
		t.Fatalf("push returned %+v, want an error for the rejected tag 1.0 (reference putManifestWithTags returns undefined)", res)
	}
	if res != nil {
		t.Errorf("result = %+v, want nil alongside the error", res)
	}
	if !strings.Contains(err.Error(), "1.0") || !strings.Contains(err.Error(), ref.Resource) {
		t.Errorf("error %q does not name the tag and resource", err)
	}
	if !strings.Contains(logs.String(), "Failed to PUT manifest for tag 1.0\n") {
		t.Errorf("log does not report the rejected tag:\n%s", logs.String())
	}

	reg.mu.Lock()
	attempted := slices.Clone(reg.putManifestAttempt)
	tagged := slices.Sorted(maps.Keys(reg.manifests))
	tokenReqs := reg.tokenRequests
	reg.mu.Unlock()
	if want := []string{"1", "1.0"}; !slices.Equal(attempted, want) {
		t.Errorf("tag PUTs attempted = %v, want %v: tags after the first failure must not be tried", attempted, want)
	}
	if want := []string{"1"}; !slices.Equal(tagged, want) {
		t.Errorf("tags in registry = %v, want %v (tags before the failure stay pushed)", tagged, want)
	}
	if tokenReqs == 0 {
		t.Error("token endpoint never called; the Bearer auth loop did not run")
	}
}

func TestPushArtifact_AllTagsFail(t *testing.T) {
	reg := newFakeOCIRegistry()
	reg.failAllManifests = true
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, nil)

	if _, err := client.PushArtifact(t.Context(), ref, writeTarball(t, "layer"), []string{"1", "1.0.0", "latest"}, "feature", nil); err == nil {
		t.Fatal("expected an error when all tags fail, got nil")
	}
}

func TestPushArtifact_BlobExistsSkipsReupload(t *testing.T) {
	reg := newFakeOCIRegistry()
	registry := reg.start(t)

	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, nil)
	tgz := writeTarball(t, "same-layer-bytes")

	if _, err := client.PushArtifact(t.Context(), ref, tgz, []string{"1.0.0"}, "feature", nil); err != nil {
		t.Fatalf("first push failed: %v", err)
	}
	reg.mu.Lock()
	uploadsAfterFirst := reg.blobUploads
	reg.mu.Unlock()
	if uploadsAfterFirst == 0 {
		t.Fatal("first push uploaded no blobs")
	}

	ref2, _ := ParseRef(registry + "/ns/hello:1.0.1")
	if _, err := client.PushArtifact(t.Context(), ref2, tgz, []string{"1.0.1"}, "feature", nil); err != nil {
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
		res, err := client.PushCollectionMetadata(t.Context(), ref, collPath)
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
		if _, err := client.PushCollectionMetadata(t.Context(), ref, collPath); err == nil {
			t.Fatal("expected error when the collection manifest is rejected, got nil")
		}
	})

	t.Run("missing metadata file", func(t *testing.T) {
		reg := newFakeOCIRegistry()
		registry := reg.start(t)
		ref, _ := ParseRef(registry + "/me/features")
		client := NewClient(log.Null, nil)
		if _, err := client.PushCollectionMetadata(t.Context(), ref, filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("expected error for a missing metadata file, got nil")
		}
	})
}
