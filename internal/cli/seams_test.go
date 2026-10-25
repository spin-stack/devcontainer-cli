package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/oci"
)

// captureOutput is a test Output that routes both streams into buffers, letting
// tests assert on command output without swapping the global os.Stdout.
type captureOutput struct {
	out bytes.Buffer
	err bytes.Buffer
}

func (c *captureOutput) Stdout() io.Writer { return &c.out }
func (c *captureOutput) Stderr() io.Writer { return &c.err }

// fakeRegistry is a hand-written oci.Registry double. PushArtifact fails for any
// resource whose id is in failIDs, exercising the partial-publish error path in
// publishCollectionWith without contacting a real registry.
type fakeRegistry struct {
	published      map[string][]string // resource -> already-published tags
	tagListErr     map[string]error    // resource -> error to return from GetPublishedTags*
	failIDs        map[string]bool
	pushed         []string            // resources successfully pushed (for assertions)
	pushedTags     map[string][]string // resource -> tags requested on push
	collectionPush int                 // times PushCollectionMetadata was called
	collectionRef  string              // the resource of the last collection push
	failCollection bool                // when true, PushCollectionMetadata errors
}

func (f *fakeRegistry) FetchManifest(ref *oci.Ref, expectedDigest string) (*oci.ManifestContainer, error) {
	return nil, fmt.Errorf("FetchManifest not used in this test")
}

func (f *fakeRegistry) FetchBlob(ref *oci.Ref, digest string) ([]byte, error) {
	return nil, fmt.Errorf("FetchBlob not used in this test")
}

func (f *fakeRegistry) GetPublishedTags(ref *oci.Ref) ([]string, error) {
	return f.GetPublishedTagsContext(context.Background(), ref)
}

func (f *fakeRegistry) GetPublishedTagsContext(_ context.Context, ref *oci.Ref) ([]string, error) {
	if f.tagListErr != nil {
		if err, ok := f.tagListErr[ref.Resource]; ok {
			return nil, err
		}
	}
	return f.published[ref.Resource], nil
}

func (f *fakeRegistry) PushArtifact(ctx context.Context, ref *oci.Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*oci.PushResult, error) {
	if f.failIDs[ref.ID] {
		return nil, fmt.Errorf("simulated push failure for %s", ref.ID)
	}
	f.pushed = append(f.pushed, ref.Resource)
	if f.pushedTags != nil {
		f.pushedTags[ref.Resource] = tags
	}
	return &oci.PushResult{Digest: "sha256:deadbeef", PublishedTags: tags}, nil
}

func (f *fakeRegistry) PushCollectionMetadata(ctx context.Context, ref *oci.Ref, collectionJSONPath string) (*oci.PushResult, error) {
	f.collectionPush++
	f.collectionRef = ref.Resource
	if f.failCollection {
		return nil, fmt.Errorf("simulated collection metadata push failure")
	}
	return &oci.PushResult{Digest: "sha256:cafef00d"}, nil
}

// writeSeamFeature writes a minimal feature source folder (metadata + install
// script) under <srcDir>/<id>.
func writeSeamFeature(t *testing.T, srcDir, id, version string) {
	t.Helper()
	writeFeature(t, srcDir, id, fmt.Sprintf(`{"id":%q,"version":%q,"name":%q}`, id, version, id))
	if err := os.WriteFile(filepath.Join(srcDir, id, "install.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPublishCollectionStopsAtTheFirstFailure proves the oci.Registry and
// cli.Output seams work together: a fake registry fails one feature's push, and
// publish ends there with exit 1 and no result JSON, as TS featuresCLI/publish.ts
// does (`process.exit(1)` after "(!) ERR: Failed to publish"). The features are
// published in collection order, so "bad" stops it before "good".
func TestPublishCollectionStopsAtTheFirstFailure(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")
	writeSeamFeature(t, src, "bad", "1.0.0")

	reg := &fakeRegistry{
		published: map[string][]string{},
		failIDs:   map[string]bool{"bad": true},
	}
	out := &captureOutput{}

	err := publishCollectionWith(t.Context(), out, reg, filepath.Join(target, "src"), "ghcr.io", "me/features", "feature", "info")
	if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 {
		t.Fatalf("err = %v, want exit code 1", err)
	}
	if len(reg.pushed) != 0 || reg.collectionPush != 0 {
		t.Fatalf("pushed = %v, collection pushes = %d: publish must stop at the first failure", reg.pushed, reg.collectionPush)
	}
	if out.out.Len() != 0 {
		t.Fatalf("stdout = %q, want no result after a failure", out.out.String())
	}
}
