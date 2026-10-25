package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// TestPublishCollection_AbortsOnTagListError covers the finding that publish must
// NOT treat a failed tag lookup as an empty registry: a non-404 error (auth, 5xx,
// network) has to abort the item — otherwise mobile aliases would be recomputed
// off an empty list and could clobber existing ones.
func TestPublishCollection_AbortsOnTagListError(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
		published: map[string][]string{},
		failIDs:   map[string]bool{},
		tagListErr: map[string]error{
			"ghcr.io/me/features/good": &errcode.ErrorResponse{StatusCode: 500},
		},
	}
	out := &captureOutput{}

	err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info")
	if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 {
		t.Fatalf("err = %v, want exit code 1 from the tag-list error (TS getPublishedTags -> process.exit(1))", err)
	}
	// The artifact must NOT have been pushed — we aborted before computing tags.
	if len(reg.pushed) != 0 {
		t.Fatalf("artifact pushed despite a tag-list error: %v", reg.pushed)
	}
}

// TestPublishCollection_TreatsNotFoundAsFirstPublish covers the complementary
// case: a 404 from the tag listing means the repository does not exist yet, so
// the item publishes normally as a first publish.
func TestPublishCollection_TreatsNotFoundAsFirstPublish(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
		published: map[string][]string{},
		failIDs:   map[string]bool{},
		tagListErr: map[string]error{
			"ghcr.io/me/features/good": &errcode.ErrorResponse{StatusCode: 404},
		},
	}
	out := &captureOutput{}

	if err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info"); err != nil {
		t.Fatalf("404 tag listing should be a clean first publish, got: %v", err)
	}
	if len(reg.pushed) != 1 || !strings.HasSuffix(reg.pushed[0], "/good") {
		t.Fatalf("pushed = %v, want [.../good]", reg.pushed)
	}
}

// TestPublishCollection_CollectionMetadataFailureExits: every item publishes but
// the collection-metadata push fails, and publish exits 1 without printing the
// result, as TS doPublishMetadata's caller does ("(!) ERR: Failed to publish
// '<registry>/<namespace>'", process.exit(1) before console.log).
func TestPublishCollection_CollectionMetadataFailureExits(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
		published:      map[string][]string{},
		failIDs:        map[string]bool{},
		failCollection: true,
	}
	out := &captureOutput{}

	err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info")
	if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 {
		t.Fatalf("err = %v, want exit code 1 from the collection push", err)
	}
	if reg.collectionPush != 1 {
		t.Fatalf("collection metadata push attempts = %d, want 1", reg.collectionPush)
	}
	if len(reg.pushed) != 1 || !strings.HasSuffix(reg.pushed[0], "/good") {
		t.Fatalf("pushed = %v, want [.../good]", reg.pushed)
	}
	if out.out.Len() != 0 {
		t.Fatalf("stdout = %q, want no result after the collection push failed", out.out.String())
	}
}

// A namespace of one segment is a valid collection (TS getCollectionRef takes
// the namespace as the whole path), so its metadata is published to
// <registry>/<namespace>. Go used to parse it as an item reference, fail, and
// skip the collection metadata silently.
func TestPublishCollection_SingleSegmentNamespacePublishesMetadata(t *testing.T) {
	target := t.TempDir()
	writeSeamFeature(t, filepath.Join(target, "src"), "good", "1.0.0")
	reg := &fakeRegistry{published: map[string][]string{}, failIDs: map[string]bool{}}
	out := &captureOutput{}
	if err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "myorg", "feature", "info"); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if reg.collectionPush != 1 || reg.collectionRef != "ghcr.io/myorg" {
		t.Fatalf("collection pushes = %d to %q, want 1 to ghcr.io/myorg", reg.collectionPush, reg.collectionRef)
	}
}

// A namespace that is no valid repository path cannot name a collection: TS
// exits 1 with "(!) Could not parse provided collection identifier".
func TestPublishCollection_InvalidCollectionNamespaceExits(t *testing.T) {
	target := t.TempDir()
	writeSeamFeature(t, filepath.Join(target, "src"), "good", "1.0.0")
	reg := &fakeRegistry{published: map[string][]string{}, failIDs: map[string]bool{}}
	out := &captureOutput{}
	err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features/", "feature", "info")
	if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 {
		t.Fatalf("err = %v, want exit code 1", err)
	}
	if reg.collectionPush != 0 || out.out.Len() != 0 {
		t.Fatalf("collection pushes = %d, stdout = %q: nothing is published for an unparsable collection", reg.collectionPush, out.out.String())
	}
}

// TestPublishCollection_SkipsAlreadyPublishedVersion covers the skip branch:
// when the exact version already exists in the registry, publishOne must NOT push
// the artifact again, must NOT count a failure, and must still report the item
// (as {}), matching the TS CLI's "already exists, skipping" path.
func TestPublishCollection_SkipsAlreadyPublishedVersion(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
		// The registry already has 1.0.0 for this resource.
		published: map[string][]string{"ghcr.io/me/features/good": {"1.0.0"}},
		failIDs:   map[string]bool{},
	}
	out := &captureOutput{}

	if err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info"); err != nil {
		t.Fatalf("skip path should not error: %v", err)
	}
	if len(reg.pushed) != 0 {
		t.Fatalf("artifact was pushed despite the version already existing: %v", reg.pushed)
	}

	// TS reports an item it skipped as an empty object.
	if got := strings.TrimSpace(out.out.String()); got != `{"good":{}}` {
		t.Fatalf("stdout = %s, want {\"good\":{}}", got)
	}
}

// TestPublishCollection_RepublishesLegacyIds covers the feature-only legacyIds
// aliasing: a feature with legacyIds is republished under each alias resource, so
// old references keep resolving. Templates do not get this treatment.
func TestPublishCollection_RepublishesLegacyIds(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeFeature(t, src, "modern", `{"id":"modern","version":"2.0.0","name":"Modern","legacyIds":["old-a","old-b"]}`)
	os.WriteFile(filepath.Join(src, "modern", "install.sh"), []byte("#!/bin/sh\n"), 0o755)

	reg := &fakeRegistry{
		published:  map[string][]string{},
		failIDs:    map[string]bool{},
		pushedTags: map[string][]string{},
	}
	out := &captureOutput{}

	if err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info"); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	want := []string{
		"ghcr.io/me/features/modern",
		"ghcr.io/me/features/old-a",
		"ghcr.io/me/features/old-b",
	}
	for _, res := range want {
		if _, ok := reg.pushedTags[res]; !ok {
			t.Errorf("expected %s to be published (legacy alias republish)", res)
		}
	}
	if len(reg.pushed) != len(want) {
		t.Errorf("pushed %d resources, want %d: %v", len(reg.pushed), len(want), reg.pushed)
	}

	// TS reports the aliases inside the item as publishedLegacyIds, not as items.
	var result map[string]map[string]any
	if err := json.Unmarshal(out.out.Bytes(), &result); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("result = %v, want only the modern id", result)
	}
	if got := fmt.Sprint(result["modern"]["publishedLegacyIds"]); got != "[old-a old-b]" {
		t.Errorf("publishedLegacyIds = %s, want [old-a old-b]", got)
	}
}
