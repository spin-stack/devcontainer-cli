package cli

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

func resolveMetadata(t *testing.T, reg oci.Registry, basePath string, lockfile *features.Lockfile, id string, opts any) (*features.Set, error) {
	t.Helper()
	return newMetadataProcessFeature(reg, log.Null, basePath, lockfile)(&features.FNode{UserFeatureID: id, Options: opts})
}

// A local Feature's metadata is its devcontainer-feature.json, its id the
// user's reference when the file declares none, and its value the options.
func TestMetadataProcessFeatureLocal(t *testing.T) {
	base := t.TempDir()
	writeFeature(t, base, "named", `{"id":"declared","dependsOn":{"./other":{}}}`)
	writeFeature(t, base, "anonymous", `{"version":"1.0.0"}`)
	opts := map[string]any{"flag": true}

	set, err := resolveMetadata(t, nil, base, nil, "./named", opts)
	if err != nil {
		t.Fatal(err)
	}
	if f := set.Features[0]; f.ID != "declared" || !reflect.DeepEqual(f.Value, opts) || f.DependsOn["./other"] == nil {
		t.Errorf("feature = %+v, want id declared, the options and the dependsOn", f)
	}
	set, err = resolveMetadata(t, nil, base, nil, "./anonymous", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Features[0].ID; got != "./anonymous" {
		t.Errorf("id = %q, want the reference ./anonymous", got)
	}
	if _, err := resolveMetadata(t, nil, base, nil, "./missing", nil); err == nil || !strings.HasPrefix(err.Error(), "ERR: Feature './missing' could not be processed.") {
		t.Errorf("missing local Feature: err = %v", err)
	}
}

// A direct tarball's metadata is the devcontainer-feature.json inside it.
func TestMetadataProcessFeatureTarball(t *testing.T) {
	opts := map[string]any{"v": "1"}
	uri := serveBytes(t, featureTgzBytes(t, map[string]string{"devcontainer-feature.json": `{"id":"tb","installsAfter":["x"]}`}), http.StatusOK) + "/f.tgz"
	set, err := resolveMetadata(t, nil, "", nil, uri, opts)
	if err != nil {
		t.Fatal(err)
	}
	if f := set.Features[0]; f.ID != "tb" || !reflect.DeepEqual(f.InstallsAfter, []string{"x"}) || !reflect.DeepEqual(f.Value, opts) {
		t.Errorf("feature = %+v", f)
	}
	missing := serveBytes(t, nil, http.StatusNotFound) + "/f.tgz"
	if _, err := resolveMetadata(t, nil, "", nil, missing, nil); err == nil || !strings.HasPrefix(err.Error(), "ERR: Feature '"+missing+"' could not be processed.") {
		t.Errorf("unreachable tarball: err = %v", err)
	}
	notTar := serveBytes(t, []byte("not a tarball"), http.StatusOK) + "/f.tgz"
	if _, err := resolveMetadata(t, nil, "", nil, notTar, nil); err == nil {
		t.Error("a tarball that does not extract must fail")
	}
}

// An OCI Feature's metadata is the manifest annotation, else (absent or empty)
// the devcontainer-feature.json of its first layer; its id is the annotation's,
// else the reference's.
func TestMetadataProcessFeatureOCI(t *testing.T) {
	const id = "ghcr.io/owner/coll/hello:1"
	layer := map[string]string{"devcontainer-feature.json": `{"id":"from-layer","dependsOn":{"ghcr.io/owner/coll/layer-dep:1":{}}}`}
	annotation := `{"id":"from-annotation","dependsOn":{"ghcr.io/owner/coll/annotation-dep:1":{}}}`
	empty := ""
	opts := map[string]any{"o": "v"}

	for name, tc := range map[string]struct {
		annotation      *string
		wantID, wantDep string
	}{
		"annotation":       {&annotation, "from-annotation", "ghcr.io/owner/coll/annotation-dep:1"},
		"no annotation":    {nil, "hello", "ghcr.io/owner/coll/layer-dep:1"},
		"empty annotation": {&empty, "hello", "ghcr.io/owner/coll/layer-dep:1"},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newOCIFeatureRegistry()
			manifest := reg.add(t, "ghcr.io/owner/coll/hello:1", layer, tc.annotation)
			set, err := resolveMetadata(t, reg, "", nil, id, opts)
			if err != nil {
				t.Fatal(err)
			}
			f := set.Features[0]
			if f.ID != tc.wantID || f.DependsOn[tc.wantDep] == nil || len(f.DependsOn) != 1 || !reflect.DeepEqual(f.Value, opts) {
				t.Errorf("feature = %+v, want id %q depending on %q", f, tc.wantID, tc.wantDep)
			}
			if set.ComputedDigest != manifest.ContentDigest || set.SourceInfo.(*features.OCISource).Resource != "ghcr.io/owner/coll/hello" {
				t.Errorf("set = %+v", set)
			}
		})
	}

	t.Run("layer unavailable", func(t *testing.T) {
		reg := newOCIFeatureRegistry()
		reg.manifests["ghcr.io/owner/coll/hello:1"] = &oci.ManifestContainer{Manifest: &oci.Manifest{}}
		m := reg.add(t, "ghcr.io/owner/coll/other:1", layer, nil)
		reg.manifests["ghcr.io/owner/coll/broken:1"] = &oci.ManifestContainer{Manifest: &oci.Manifest{Layers: []oci.Layer{{Digest: "sha256:" + strings.Repeat("e", 64)}, m.Manifest.Layers[0]}}}
		for _, ref := range []string{"ghcr.io/owner/coll/hello:1", "ghcr.io/owner/coll/broken:1"} {
			set, err := resolveMetadata(t, reg, "", nil, ref, nil)
			if err != nil {
				t.Fatal(err)
			}
			if f := set.Features[0]; len(f.DependsOn) != 0 {
				t.Errorf("%s: dependsOn = %v, want none without a readable first layer", ref, f.DependsOn)
			}
		}
	})

	t.Run("pinned by the lockfile", func(t *testing.T) {
		digest := "sha256:" + strings.Repeat("cd", 32)
		reg := newOCIFeatureRegistry()
		reg.add(t, "ghcr.io/owner/coll/hello@"+digest, layer, nil)
		lf := &features.Lockfile{Features: map[string]features.LockfileEntry{id: {Integrity: digest}}}
		if _, err := resolveMetadata(t, reg, "", lf, id, nil); err != nil {
			t.Fatal(err)
		}
		if want := []string{"ghcr.io/owner/coll/hello@" + digest}; !reflect.DeepEqual(reg.fetched, want) {
			t.Errorf("fetched %v, want %v", reg.fetched, want)
		}
	})

	for name, tc := range map[string]struct {
		id    string
		setup func(r *ociFeatureRegistry)
		want  string
	}{
		"unparseable reference": {id: ".bad/owner/hello", want: "ERR: Feature '.bad/owner/hello' could not be processed."},
		"manifest unavailable":  {id: id, want: "You may not have permission to access this Feature"},
		"manifest empty": {id: id, setup: func(r *ociFeatureRegistry) {
			r.manifests["ghcr.io/owner/coll/hello:1"] = &oci.ManifestContainer{}
		}, want: "You may not have permission to access this Feature"},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newOCIFeatureRegistry()
			if tc.setup != nil {
				tc.setup(reg)
			}
			if _, err := resolveMetadata(t, reg, "", nil, tc.id, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// `features info` draws the root and its dependencies; a graph that cannot be
// built is an empty flowchart.
func TestRenderDependencyMermaid(t *testing.T) {
	reg := newOCIFeatureRegistry()
	root := `{"id":"root","dependsOn":{"ghcr.io/owner/coll/dep:1":{}}}`
	reg.add(t, "ghcr.io/owner/coll/root:1", map[string]string{"devcontainer-feature.json": root}, &root)
	reg.add(t, "ghcr.io/owner/coll/dep:1", map[string]string{"devcontainer-feature.json": `{"id":"dep"}`}, nil)

	got := renderDependencyMermaid(reg, log.Null, []string{"ghcr.io/owner/coll/root:1"})
	if !strings.HasPrefix(got, "flowchart\n") || !strings.Contains(got, "[ghcr.io/owner/coll/root:1<br/>") || !strings.Contains(got, "--> ") || !strings.Contains(got, "[ghcr.io/owner/coll/dep:1<br/>") {
		t.Errorf("mermaid = %q, want the root with an edge to its dependency", got)
	}
	if got := renderDependencyMermaid(reg, log.Null, []string{"ghcr.io/owner/coll/missing:1"}); got != "flowchart\n" {
		t.Errorf("unresolvable root: mermaid = %q, want an empty flowchart", got)
	}
}

// A tarball without devcontainer-feature.json has empty metadata, not an error.
func TestFeatureMetadataFromTarballWithoutMetadata(t *testing.T) {
	meta, err := featureMetadataFromTarball(featureTgzBytes(t, map[string]string{"install.sh": "#!/bin/sh\n"}))
	if err != nil || !reflect.DeepEqual(meta, features.Feature{}) {
		t.Fatalf("meta = %+v, err = %v; want empty metadata", meta, err)
	}
}
