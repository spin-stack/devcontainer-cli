package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishCollection_CollectionMetadataFailureCounts complements the
// item-level partial-failure test (seams_test.go): here every ITEM publishes
// fine but the SEPARATE collection-metadata push fails. That failure must be
// counted and surfaced as the command error, while the successfully published
// items still appear in the result JSON. This exercises the distinct
// "collection-metadata push" failure path, separate from item-level failures.
func TestPublishCollection_CollectionMetadataFailureCounts(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
		published:      map[string][]string{},
		failIDs:        map[string]bool{},
		failCollection: true,
	}
	out := &captureOutput{}

	err := publishCollectionWith(out, reg, target, "ghcr.io", "me/features", "feature", "info")
	if err == nil || !strings.Contains(err.Error(), "publish operation(s) failed") {
		t.Fatalf("err = %v, want a partial-publish failure from the collection push", err)
	}
	if reg.collectionPush != 1 {
		t.Fatalf("collection metadata push attempts = %d, want 1", reg.collectionPush)
	}
	// The item still published and is reported despite the collection failure.
	if len(reg.pushed) != 1 || !strings.HasSuffix(reg.pushed[0], "/good") {
		t.Fatalf("pushed = %v, want [.../good]", reg.pushed)
	}
	var result map[string]any
	if jerr := json.Unmarshal(out.out.Bytes(), &result); jerr != nil {
		t.Fatalf("stdout not JSON: %v", jerr)
	}
	if _, ok := result["good"]; !ok {
		t.Fatalf("result missing 'good': %v", result)
	}
}

// TestPublishCollection_SkipsAlreadyPublishedVersion covers the skip branch:
// when the exact version already exists in the registry, publishOne must NOT push
// the artifact again, must NOT count a failure, and must still report the item
// (with empty tags), matching the TS CLI's "already exists, skipping" path.
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

	if err := publishCollectionWith(out, reg, target, "ghcr.io", "me/features", "feature", "info"); err != nil {
		t.Fatalf("skip path should not error: %v", err)
	}
	if len(reg.pushed) != 0 {
		t.Fatalf("artifact was pushed despite the version already existing: %v", reg.pushed)
	}

	var result map[string]struct {
		PublishedTags []string `json:"publishedTags"`
	}
	if err := json.Unmarshal(out.out.Bytes(), &result); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	good, ok := result["good"]
	if !ok {
		t.Fatalf("skipped item should still be reported: %v", result)
	}
	if len(good.PublishedTags) != 0 {
		t.Fatalf("skipped item should report no published tags, got %v", good.PublishedTags)
	}
}

// TestPublishCollection_RepublishesLegacyIds covers the feature-only legacyIds
// aliasing: a feature with legacyIds is republished under each alias resource, so
// old references keep resolving. Templates do not get this treatment.
func TestPublishCollection_RepublishesLegacyIds(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeFeature(t, src, "modern", `{"id":"modern","version":"2.0.0","name":"Modern","legacyIds":["old-a","old-b"]}`)

	reg := &fakeRegistry{
		published:  map[string][]string{},
		failIDs:    map[string]bool{},
		pushedTags: map[string][]string{},
	}
	out := &captureOutput{}

	if err := publishCollectionWith(out, reg, target, "ghcr.io", "me/features", "feature", "info"); err != nil {
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

	// The result JSON is keyed by both the modern id and each legacy alias.
	var result map[string]any
	if err := json.Unmarshal(out.out.Bytes(), &result); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	for _, id := range []string{"modern", "old-a", "old-b"} {
		if _, ok := result[id]; !ok {
			t.Errorf("result missing %q: %v", id, result)
		}
	}
}
