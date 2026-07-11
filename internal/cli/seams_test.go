package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	failIDs        map[string]bool
	pushed         []string // resources successfully pushed (for assertions)
	collectionPush int      // times PushCollectionMetadata was called
}

func (f *fakeRegistry) FetchManifest(ref *oci.Ref, expectedDigest string) (*oci.ManifestContainer, error) {
	return nil, fmt.Errorf("FetchManifest not used in this test")
}

func (f *fakeRegistry) FetchBlob(ref *oci.Ref, digest string) ([]byte, error) {
	return nil, fmt.Errorf("FetchBlob not used in this test")
}

func (f *fakeRegistry) GetPublishedTags(ref *oci.Ref) ([]string, error) {
	return f.published[ref.Resource], nil
}

func (f *fakeRegistry) PushArtifact(ref *oci.Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*oci.PushResult, error) {
	if f.failIDs[ref.ID] {
		return nil, fmt.Errorf("simulated push failure for %s", ref.ID)
	}
	f.pushed = append(f.pushed, ref.Resource)
	return &oci.PushResult{Digest: "sha256:deadbeef", PublishedTags: tags}, nil
}

func (f *fakeRegistry) PushCollectionMetadata(ref *oci.Ref, collectionJSONPath string) (*oci.PushResult, error) {
	f.collectionPush++
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

// TestPublishCollectionPartialFailure proves the oci.Registry and cli.Output
// seams work together: a fake registry drives a partial-publish (one feature
// pushes, one fails), and the captured stdout carries the result JSON — no real
// registry, no global os.Stdout swap.
func TestPublishCollectionPartialFailure(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(target, "src")
	writeSeamFeature(t, src, "good", "1.0.0")
	writeSeamFeature(t, src, "bad", "1.0.0")

	reg := &fakeRegistry{
		published: map[string][]string{},
		failIDs:   map[string]bool{"bad": true},
	}
	out := &captureOutput{}

	err := publishCollectionWith(out, reg, target, "ghcr.io", "me/features", "feature", "info")
	if err == nil {
		t.Fatal("expected a partial-publish error, got nil")
	}
	if !strings.Contains(err.Error(), "publish operation(s) failed") {
		t.Fatalf("error = %v, want partial-publish failure", err)
	}

	// The good feature was pushed; the bad one was not.
	if len(reg.pushed) != 1 || !strings.HasSuffix(reg.pushed[0], "/good") {
		t.Fatalf("pushed = %v, want exactly [.../good]", reg.pushed)
	}

	// stdout carries the result JSON keyed by the published feature id.
	var result map[string]interface{}
	if jerr := json.Unmarshal(out.out.Bytes(), &result); jerr != nil {
		t.Fatalf("stdout is not valid JSON: %v (%q)", jerr, out.out.String())
	}
	if _, ok := result["good"]; !ok {
		t.Fatalf("result missing 'good': %v", result)
	}
	if _, ok := result["bad"]; ok {
		t.Fatalf("result should not contain failed 'bad': %v", result)
	}
}
