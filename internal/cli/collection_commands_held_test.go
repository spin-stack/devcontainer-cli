package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

// pushStub is a registry whose pushes answer what the test sets per resource
// (by default every requested tag under a digest), recording each push's
// annotations.
type pushStub struct {
	fakeRegistry
	results     map[string]*oci.PushResult
	annotations map[string]string // resource -> dev.containers.metadata
}

func newPushStub() *pushStub {
	return &pushStub{
		fakeRegistry: fakeRegistry{published: map[string][]string{}, failIDs: map[string]bool{}},
		results:      map[string]*oci.PushResult{},
		annotations:  map[string]string{},
	}
}

func (s *pushStub) PushArtifact(_ context.Context, ref *oci.Ref, _ string, tags []string, _ string, annotations map[string]string) (*oci.PushResult, error) {
	s.pushed = append(s.pushed, ref.Resource)
	s.annotations[ref.Resource] = annotations["dev.containers.metadata"]
	if r, ok := s.results[ref.Resource]; ok {
		return r, nil
	}
	return &oci.PushResult{Digest: "sha256:d1", PublishedTags: tags}, nil
}

// writeCollectionFeature writes <src>/<id>/devcontainer-feature.json with the
// given metadata and an install.sh.
func writeCollectionFeature(t *testing.T, src, id, metadata string) {
	t.Helper()
	writeFeature(t, src, id, metadata)
	if err := os.WriteFile(filepath.Join(src, id, "install.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishResult(t *testing.T, out *captureOutput) map[string]publishItemResult {
	t.Helper()
	var got map[string]publishItemResult
	if err := json.Unmarshal(out.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the publish result: %v\n%s", err, out.out.String())
	}
	return got
}

func TestRequireTarget(t *testing.T) {
	if err := requireTarget(nil, nil); err == nil || err.Error() != "Not enough non-option arguments: got 0, need at least 1" {
		t.Errorf("no target: err = %v", err)
	}
	if err := requireTarget(nil, []string{"src"}); err != nil {
		t.Errorf("one target: err = %v", err)
	}
	if err := requireTarget(nil, []string{"a", "b"}); err == nil {
		t.Error("two targets: want an error")
	}
}

// package <target> packages the folder its one argument names.
func TestFeaturesPackageCommandPackagesTarget(t *testing.T) {
	src := t.TempDir()
	writeCollectionFeature(t, src, "tool", `{"id":"tool","version":"1.0.0","name":"Tool"}`)
	outDir := filepath.Join(t.TempDir(), "out")
	cmd := realFeaturesPackageCmd()
	cmd.SetArgs([]string{src, "--output-folder", outDir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "devcontainer-feature-tool.tgz")); err != nil {
		t.Errorf("archive missing: %v", err)
	}
}

// publish <target> checks --namespace before it reaches a registry, for both
// collection types, and validates --log-level next.
func TestPublishCommandsRequireNamespace(t *testing.T) {
	for name, cmd := range map[string]func() *cobra.Command{"features": realFeaturesPublishCmd, "templates": realTemplatesPublishCmd} {
		c := cmd()
		c.SetArgs([]string{t.TempDir()})
		c.SetOut(&strings.Builder{})
		c.SetErr(&strings.Builder{})
		if err := c.Execute(); err == nil || err.Error() != "Missing required argument: namespace" {
			t.Errorf("%s publish: err = %v, want the missing namespace", name, err)
		}
	}
	err := publishCollection(t.Context(), t.TempDir(), "ghcr.io", "me/f", "feature", "loud")
	if err == nil || !strings.Contains(err.Error(), "log-level") {
		t.Errorf("err = %v, want the invalid log-level", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	err = publishCollection(t.Context(), missing, "ghcr.io", "me/f", "feature", "info")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v, want packaging to fail on the missing target", err)
	}
}

// A Feature collection that cannot be packaged adds TS's collection error line;
// a Template collection does not.
func TestPublishCollectionPackagingFailureLine(t *testing.T) {
	for _, c := range []struct {
		kind string
		want bool
	}{{"feature", true}, {"template", false}} {
		empty := t.TempDir()
		out := &captureOutput{}
		err := publishCollectionWith(t.Context(), out, newPushStub(), empty, "ghcr.io", "me/c", c.kind, "info")
		if exitCode(err) != 1 {
			t.Fatalf("%s: err = %v, want exit 1", c.kind, err)
		}
		if got := strings.Contains(out.err.String(), "(!) ERR: Failed to fetch devcontainer-collection.json"); got != c.want {
			t.Errorf("%s: collection error line printed = %v, want %v\n%s", c.kind, got, c.want, out.err.String())
		}
	}
}

// An item whose resource is not a valid reference stops publish, naming the
// collection type as TS does.
func TestPublishCollectionUnparsableResource(t *testing.T) {
	src := t.TempDir()
	writeCollectionFeature(t, src, "tool", `{"id":"tool","version":"1.0.0","name":"Tool"}`)
	out := &captureOutput{}
	err := publishCollectionWith(t.Context(), out, newPushStub(), src, "ghcr.io", "", "feature", "info")
	if exitCode(err) != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(out.err.String(), "(!) Could not parse provided Feature identifier: 'ghcr.io//tool'") {
		t.Errorf("stderr = %q", out.err.String())
	}
}

// The dev.containers.metadata annotation keeps the metadata's own key order.
func TestPublishCollectionAnnotationKeepsKeyOrder(t *testing.T) {
	src := t.TempDir()
	writeCollectionFeature(t, src, "tool", `{"version":"1.0.0","name":"Tool","id":"tool"}`)
	reg := newPushStub()
	if err := publishCollectionWith(t.Context(), &captureOutput{}, reg, src, "ghcr.io", "me/f", "feature", "info"); err != nil {
		t.Fatal(err)
	}
	if got, want := reg.annotations["ghcr.io/me/f/tool"], `{"version":"1.0.0","name":"Tool","id":"tool"}`; got != want {
		t.Errorf("annotation = %s, want %s", got, want)
	}
}

// An item counts as published only with both a digest and at least one tag;
// otherwise its result is empty and its legacy ids are not published. A
// published Feature with a single legacy id republishes it.
func TestPublishCollectionPublishedNeedsDigestAndTags(t *testing.T) {
	src := t.TempDir()
	writeCollectionFeature(t, src, "a", `{"id":"a","version":"1.0.0","name":"A","legacyIds":["old-a"]}`)
	writeCollectionFeature(t, src, "b", `{"id":"b","version":"1.0.0","name":"B"}`)
	writeCollectionFeature(t, src, "c", `{"id":"c","version":"1.0.0","name":"C","legacyIds":["old-c"]}`)
	reg := newPushStub()
	reg.results["ghcr.io/me/f/a"] = &oci.PushResult{Digest: "", PublishedTags: []string{"1"}}
	reg.results["ghcr.io/me/f/b"] = &oci.PushResult{Digest: "sha256:b", PublishedTags: nil}
	reg.results["ghcr.io/me/f/c"] = &oci.PushResult{Digest: "sha256:c", PublishedTags: []string{"1.0.0"}}
	out := &captureOutput{}
	if err := publishCollectionWith(t.Context(), out, reg, src, "ghcr.io", "me/f", "feature", "info"); err != nil {
		t.Fatal(err)
	}
	got := publishResult(t, out)
	if got["a"].Digest != "" || got["b"].Digest != "" {
		t.Errorf("result = %+v, want a and b empty", got)
	}
	if c := got["c"]; c.Digest != "sha256:c" || c.Version != "1.0.0" || len(c.PublishedTags) != 1 {
		t.Errorf("c = %+v, want published with one tag", c)
	}
	if ids := got["c"].PublishedLegacyIDs; len(ids) != 1 || ids[0] != "old-c" {
		t.Errorf("c publishedLegacyIds = %q, want its one legacy id", ids)
	}
	for _, r := range reg.pushed {
		if strings.HasSuffix(r, "/old-a") {
			t.Errorf("pushed %v: legacy id of an unpublished item", reg.pushed)
		}
	}
}

// A published Feature is republished under each legacy id; the result lists
// the ids that were published (digest and a tag), not the others.
func TestPublishCollectionLegacyIDs(t *testing.T) {
	src := t.TempDir()
	writeCollectionFeature(t, src, "tool", `{"id":"tool","version":"1.0.0","name":"Tool","legacyIds":["old1","old2","old3"]}`)
	reg := newPushStub()
	reg.results["ghcr.io/me/f/old2"] = &oci.PushResult{Digest: "", PublishedTags: []string{"1"}}
	reg.results["ghcr.io/me/f/old3"] = &oci.PushResult{Digest: "sha256:o3", PublishedTags: nil}
	reg.results["ghcr.io/me/f/old1"] = &oci.PushResult{Digest: "sha256:o1", PublishedTags: []string{"1.0.0"}}
	out := &captureOutput{}
	if err := publishCollectionWith(t.Context(), out, reg, src, "ghcr.io", "me/f", "feature", "info"); err != nil {
		t.Fatal(err)
	}
	if got := publishResult(t, out)["tool"].PublishedLegacyIDs; len(got) != 1 || got[0] != "old1" {
		t.Errorf("publishedLegacyIds = %q, want [old1]", got)
	}
}

// Templates have no legacy ids to republish, even when the metadata lists some.
func TestPublishCollectionTemplateIgnoresLegacyIDs(t *testing.T) {
	src := t.TempDir()
	dir := filepath.Join(src, "tmpl")
	writeFile(t, filepath.Join(dir, "devcontainer-template.json"), `{"id":"tmpl","version":"1.0.0","name":"T","legacyIds":["old"]}`, 0o644)
	writeFile(t, filepath.Join(dir, ".devcontainer.json"), `{"image":"alpine"}`, 0o644)
	reg := newPushStub()
	if err := publishCollectionWith(t.Context(), &captureOutput{}, reg, src, "ghcr.io", "me/t", "template", "info"); err != nil {
		t.Fatal(err)
	}
	if len(reg.pushed) != 1 || reg.pushed[0] != "ghcr.io/me/t/tmpl" {
		t.Errorf("pushed = %v, want only the template", reg.pushed)
	}
}
