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
	if len(reg.pushed) != 0 {
		t.Fatalf("artifact pushed despite a tag-list error: %v", reg.pushed)
	}
}

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

func TestPublishCollection_SkipsAlreadyPublishedVersion(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")

	reg := &fakeRegistry{
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

	if got := strings.TrimSpace(out.out.String()); got != `{"good":{}}` {
		t.Fatalf("stdout = %s, want {\"good\":{}}", got)
	}
}

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
