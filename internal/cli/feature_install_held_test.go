package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// featureTgzBytes is a gzipped Feature tarball holding the given files.
func featureTgzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var entries [][2]string
	for name, content := range files {
		entries = append(entries, [2]string{name, content})
	}
	data, err := os.ReadFile(writeTarGz(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ociFeatureRegistry is an oci.Registry serving Feature manifests and layers
// from memory, keyed by "<resource>:<tag>" or "<resource>@<digest>". It records
// every manifest reference asked for.
type ociFeatureRegistry struct {
	fakeRegistry
	manifests map[string]*oci.ManifestContainer
	blobs     map[string][]byte
	blobErr   error
	fetched   []string
}

func newOCIFeatureRegistry() *ociFeatureRegistry {
	return &ociFeatureRegistry{manifests: map[string]*oci.ManifestContainer{}, blobs: map[string][]byte{}}
}

func ociRefKey(ref *oci.Ref) string {
	if ref.Digest != "" {
		return ref.Resource + "@" + ref.Digest
	}
	return ref.Resource + ":" + ref.Tag
}

// add serves a Feature at key whose single layer holds files; annotation, when
// not nil, is the manifest's dev.containers.metadata.
func (r *ociFeatureRegistry) add(t *testing.T, key string, files map[string]string, annotation *string) *oci.ManifestContainer {
	t.Helper()
	blob := featureTgzBytes(t, files)
	layer := sha256Digest(blob)
	r.blobs[layer] = blob
	m := &oci.ManifestContainer{
		Manifest:      &oci.Manifest{Layers: []oci.Layer{{Digest: layer}}},
		ContentDigest: sha256Digest([]byte(key)),
	}
	if annotation != nil {
		m.Manifest.Annotations = map[string]string{"dev.containers.metadata": *annotation}
	}
	r.manifests[key] = m
	return m
}

func (r *ociFeatureRegistry) FetchManifest(ref *oci.Ref, _ string) (*oci.ManifestContainer, error) {
	key := ociRefKey(ref)
	r.fetched = append(r.fetched, key)
	if m, ok := r.manifests[key]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("manifest unknown: %s", key)
}

func (r *ociFeatureRegistry) FetchBlob(_ *oci.Ref, digest string) ([]byte, error) {
	if r.blobErr != nil {
		return nil, r.blobErr
	}
	if b, ok := r.blobs[digest]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("blob unknown: %s", digest)
}

// stageDirs hands out fresh staging folders under a temp dir, as
// fetchFeatureSetsWithOrder's nextStageDir does.
func stageDirs(t *testing.T) func() string {
	base := t.TempDir()
	n := 0
	return func() string {
		n++
		return filepath.Join(base, fmt.Sprintf("stage-%d", n))
	}
}

func installFeature(t *testing.T, reg oci.Registry, id string, opts any, lockfile *features.Lockfile) (*features.Set, error) {
	t.Helper()
	return processInstallFeature(log.Null, reg, &features.FNode{UserFeatureID: id, Options: opts}, t.TempDir(), false, lockfile, stageDirs(t))
}

// An OCI Feature's identity, version and staged content come from the
// reference and the layer's devcontainer-feature.json; the manifest's
// dev.containers.metadata annotation, when there is one, replaces every
// dependency and container property the layer declared.
func TestProcessInstallFeatureOCI(t *testing.T) {
	const id = "ghcr.io/owner/coll/hello:1"
	blobMeta := `{"id":"hello","name":"Hello","options":{"blob":{}},"dependsOn":{"blob/dep":{}},"installsAfter":["blob"],
		"legacyIds":["blob"],"init":false,"privileged":false,"capAdd":["BLOB"],"securityOpt":["blob"],"entrypoint":"/blob",
		"mounts":["blob"],"customizations":{"blob":{}}}`
	annotation := `{"legacyIds":["old-hello"],"dependsOn":{"ghcr.io/owner/coll/zdep:1":{},"ghcr.io/owner/coll/dep:1":{}},"installsAfter":["ghcr.io/owner/coll/after"],
		"options":{"greeting":{"type":"string"}},"init":true,"privileged":true,"capAdd":["SYS_PTRACE"],"securityOpt":["seccomp=unconfined"],
		"entrypoint":"/ann","mounts":["source=a,target=/a,type=volume"],"customizations":{"vscode":{"extensions":["x"]}}}`

	t.Run("annotation replaces the layer's properties", func(t *testing.T) {
		reg := newOCIFeatureRegistry()
		manifest := reg.add(t, "ghcr.io/owner/coll/hello:1", map[string]string{"devcontainer-feature.json": blobMeta, "install.sh": "#!/bin/sh\n"}, &annotation)
		set, err := installFeature(t, reg, id, map[string]any{"greeting": "hi"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(set.Features) != 1 {
			t.Fatalf("features = %d, want 1", len(set.Features))
		}
		var want features.Feature
		if err := json.Unmarshal([]byte(annotation), &want); err != nil {
			t.Fatal(err)
		}
		got := set.Features[0]
		for name, pair := range map[string][2]any{
			"legacyIds":      {got.LegacyIds, want.LegacyIds},
			"dependsOn":      {got.DependsOn, want.DependsOn},
			"dependsOnKeys":  {got.DependsOnKeys(), []string{"ghcr.io/owner/coll/zdep:1", "ghcr.io/owner/coll/dep:1"}},
			"installsAfter":  {got.InstallsAfter, want.InstallsAfter},
			"options":        {got.Options, want.Options},
			"init":           {got.Init, want.Init},
			"privileged":     {got.Privileged, want.Privileged},
			"capAdd":         {got.CapAdd, want.CapAdd},
			"securityOpt":    {got.SecurityOpt, want.SecurityOpt},
			"entrypoint":     {got.Entrypoint, want.Entrypoint},
			"mounts":         {got.Mounts, want.Mounts},
			"customizations": {got.Customizations, want.Customizations},
		} {
			if !reflect.DeepEqual(pair[0], pair[1]) {
				t.Errorf("%s = %#v, want the annotation's %#v", name, pair[0], pair[1])
			}
		}
		if got.ID != "hello" || got.Name != "Hello" || got.Version != "1" {
			t.Errorf("id/name/version = %q/%q/%q, want hello/Hello/1 (the tag, as the layer has no version)", got.ID, got.Name, got.Version)
		}
		if _, err := os.Stat(filepath.Join(got.CachePath, "install.sh")); err != nil {
			t.Errorf("content not staged: %v", err)
		}
		src, ok := set.SourceInfo.(*features.OCISource)
		if !ok {
			t.Fatalf("source = %T", set.SourceInfo)
		}
		ref := src.FeatureRef.(map[string]string)
		if ref["owner"] != "owner" || ref["path"] != "owner/coll/hello" || ref["tag"] != "1" {
			t.Errorf("featureRef = %v", ref)
		}
		if src.UserFeatureIdWithoutVersion != "ghcr.io/owner/coll/hello" || set.ComputedDigest != manifest.ContentDigest {
			t.Errorf("userFeatureIdWithoutVersion = %q, computedDigest = %q", src.UserFeatureIdWithoutVersion, set.ComputedDigest)
		}
	})

	t.Run("without annotation the layer's metadata stands", func(t *testing.T) {
		reg := newOCIFeatureRegistry()
		reg.add(t, "ghcr.io/owner/coll/hello:1", map[string]string{"devcontainer-feature.json": `{"id":"hello","version":"2.3.0","capAdd":["BLOB"]}`}, nil)
		set, err := installFeature(t, reg, id, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := set.Features[0]; got.Version != "2.3.0" || !reflect.DeepEqual(got.CapAdd, []string{"BLOB"}) {
			t.Errorf("version = %q capAdd = %v, want the layer's 2.3.0 [BLOB]", got.Version, got.CapAdd)
		}
	})
}

// A lockfile entry with an integrity pins the manifest fetch to that digest;
// an entry without one, or for another Feature, leaves the tag.
func TestProcessInstallFeatureOCIPinsToTheLockfile(t *testing.T) {
	const id = "ghcr.io/owner/coll/hello:1"
	digest := "sha256:" + strings.Repeat("cd", 32)
	files := map[string]string{"devcontainer-feature.json": `{"id":"hello"}`}
	reg := newOCIFeatureRegistry()
	reg.add(t, "ghcr.io/owner/coll/hello@"+digest, files, nil)
	reg.add(t, "ghcr.io/owner/coll/hello:1", files, nil)

	for name, tc := range map[string]struct {
		lockfile *features.Lockfile
		want     string
	}{
		"pinned":       {&features.Lockfile{Features: map[string]features.LockfileEntry{id: {Integrity: digest}}}, "ghcr.io/owner/coll/hello@" + digest},
		"no integrity": {&features.Lockfile{Features: map[string]features.LockfileEntry{id: {Version: "1.0.0"}}}, "ghcr.io/owner/coll/hello:1"},
		"other entry":  {&features.Lockfile{Features: map[string]features.LockfileEntry{"ghcr.io/x/y/z:1": {Integrity: digest}}}, "ghcr.io/owner/coll/hello:1"},
	} {
		t.Run(name, func(t *testing.T) {
			reg.fetched = nil
			if _, err := installFeature(t, reg, id, nil, tc.lockfile); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reg.fetched, []string{tc.want}) {
				t.Errorf("fetched %v, want %v", reg.fetched, []string{tc.want})
			}
		})
	}
}

// A known legacy shorthand with a version resolves to its registry Feature.
func TestProcessInstallFeatureLegacyShorthandWithVersion(t *testing.T) {
	reg := newOCIFeatureRegistry()
	reg.add(t, "ghcr.io/devcontainers/features/node:1", map[string]string{"devcontainer-feature.json": `{"id":"node"}`}, nil)
	set, err := installFeature(t, reg, "node:1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.SourceInfo.(*features.OCISource).Resource; got != "ghcr.io/devcontainers/features/node" {
		t.Errorf("resource = %q", got)
	}
}

// Every way an OCI Feature can fail to be fetched or staged is an error naming
// the Feature.
func TestProcessInstallFeatureOCIErrors(t *testing.T) {
	const id = "ghcr.io/owner/coll/hello:1"
	good := map[string]string{"devcontainer-feature.json": `{"id":"hello"}`}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id      string
		setup   func(r *ociFeatureRegistry)
		stage   func() string
		wantErr string
	}{
		"unparseable reference": {id: ".bad/owner/hello", wantErr: "ERR: Feature '.bad/owner/hello' could not be processed."},
		"manifest unavailable":  {id: "ghcr.io/owner/coll/missing:1", wantErr: "You may not have permission to access this Feature"},
		"no layers": {setup: func(r *ociFeatureRegistry) {
			r.manifests["ghcr.io/owner/coll/hello:1"] = &oci.ManifestContainer{Manifest: &oci.Manifest{}}
		}, wantErr: "The Feature manifest has no layers."},
		"blob unavailable": {setup: func(r *ociFeatureRegistry) {
			r.add(t, "ghcr.io/owner/coll/hello:1", good, nil)
			r.blobErr = errors.New("boom")
		}, wantErr: `fetch feature "ghcr.io/owner/coll/hello:1" blob: boom`},
		"blob not a tarball": {setup: func(r *ociFeatureRegistry) {
			m := r.add(t, "ghcr.io/owner/coll/hello:1", good, nil)
			r.blobs[m.Manifest.Layers[0].Digest] = []byte("not a tarball")
		}, wantErr: `extract feature "ghcr.io/owner/coll/hello:1"`},
		"staging folder not writable": {setup: func(r *ociFeatureRegistry) {
			r.add(t, "ghcr.io/owner/coll/hello:1", good, nil)
		}, stage: func() string { return filepath.Join(blocker, "stage") }, wantErr: "write feature tarball"},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newOCIFeatureRegistry()
			if tc.setup != nil {
				tc.setup(reg)
			}
			featureID := tc.id
			if featureID == "" {
				featureID = id
			}
			stage := tc.stage
			if stage == nil {
				stage = stageDirs(t)
			}
			_, err := processInstallFeature(log.Null, reg, &features.FNode{UserFeatureID: featureID}, t.TempDir(), false, nil, stage)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// serveBytes serves body at every path and returns the server's URL.
func serveBytes(t *testing.T, body []byte, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A direct-tarball Feature takes its id from devcontainer-feature.json, else
// from the tarball's devcontainer-feature-<id>.tgz name; its name, when the file
// has none, is the user's reference; its digest is recorded for the lockfile.
func TestProcessInstallFeatureTarball(t *testing.T) {
	for name, tc := range map[string]struct {
		files            map[string]string
		path             string
		wantID, wantName string
	}{
		"metadata id, no name": {files: map[string]string{"devcontainer-feature.json": `{"id":"meta-id"}`}, path: "/devcontainer-feature-file-id.tgz", wantID: "meta-id"},
		"metadata name kept":   {files: map[string]string{"devcontainer-feature.json": `{"id":"meta-id","name":"Pretty"}`}, path: "/f.tgz", wantID: "meta-id", wantName: "Pretty"},
		"no metadata":          {files: map[string]string{"install.sh": "#!/bin/sh\n"}, path: "/devcontainer-feature-file-id.tgz", wantID: "file-id"},
	} {
		t.Run(name, func(t *testing.T) {
			blob := featureTgzBytes(t, tc.files)
			uri := serveBytes(t, blob, http.StatusOK) + tc.path
			set, err := installFeature(t, nil, uri, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantName := tc.wantName
			if wantName == "" {
				wantName = uri
			}
			f := set.Features[0]
			if f.ID != tc.wantID || f.Name != wantName {
				t.Errorf("id/name = %q/%q, want %q/%q", f.ID, f.Name, tc.wantID, wantName)
			}
			if set.ComputedDigest != sha256Digest(blob) {
				t.Errorf("computedDigest = %q, want %q", set.ComputedDigest, sha256Digest(blob))
			}
		})
	}
}

func TestProcessInstallFeatureTarballErrors(t *testing.T) {
	blob := featureTgzBytes(t, map[string]string{"devcontainer-feature.json": `{"id":"x"}`})
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("download fails", func(t *testing.T) {
		uri := serveBytes(t, nil, http.StatusNotFound) + "/f.tgz"
		_, err := installFeature(t, nil, uri, nil, nil)
		if err == nil || !strings.HasPrefix(err.Error(), "ERR: Feature '"+uri+"' could not be processed.") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("digest differs from the lockfile", func(t *testing.T) {
		uri := serveBytes(t, blob, http.StatusOK) + "/f.tgz"
		lf := &features.Lockfile{Features: map[string]features.LockfileEntry{uri: {Integrity: "sha256:" + strings.Repeat("0", 64)}}}
		_, err := installFeature(t, nil, uri, nil, lf)
		if _, ok := errors.AsType[*digestMismatchError](err); !ok {
			t.Fatalf("err = %v, want a digest mismatch", err)
		}
	})
	t.Run("not a tarball", func(t *testing.T) {
		uri := serveBytes(t, []byte("not a tarball"), http.StatusOK) + "/f.tgz"
		_, err := installFeature(t, nil, uri, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "extract feature") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staging folder not writable", func(t *testing.T) {
		uri := serveBytes(t, blob, http.StatusOK) + "/f.tgz"
		_, err := processInstallFeature(log.Null, nil, &features.FNode{UserFeatureID: uri}, t.TempDir(), false, nil, func() string { return filepath.Join(blocker, "stage") })
		if err == nil || !strings.Contains(err.Error(), "write feature tarball") {
			t.Fatalf("err = %v", err)
		}
	})
}

// The id of a tarball comes from its URL's path: a fragment or query is not
// part of the name.
func TestTarballFeatureIDUsesTheURLPath(t *testing.T) {
	for uri, want := range map[string]string{
		"https://example.com/r/devcontainer-feature-go.tgz#frag/x": "go",
		"https://example.com/r/plain.tgz":                          "plain.tgz",
		"https://[::1/devcontainer-feature-bad.tgz":                "bad",
	} {
		if got := tarballFeatureID(uri); got != want {
			t.Errorf("tarballFeatureID(%q) = %q, want %q", uri, got, want)
		}
	}
}

// Root Features are resolved in sorted order, so of two broken ones the error
// always names the same.
func TestFetchFeatureSetsReportsTheFirstSortedFailure(t *testing.T) {
	for range 5 {
		_, err := fetchFeatureSets(t.Context(), log.Null, map[string]any{"./b-missing": true, "./a-missing": true, "./c-missing": true}, t.TempDir(), false)
		if err == nil || !strings.Contains(err.Error(), "'./a-missing'") {
			t.Fatalf("err = %v, want the failure of ./a-missing", err)
		}
	}
}

// A staged Feature whose folder cannot be moved or copied fails the staging.
func TestRealignFeatureDirsFailsOnMissingStage(t *testing.T) {
	dst := t.TempDir()
	set := &features.Set{Features: []features.Feature{{ID: "x", CachePath: filepath.Join(dst, ".stage-gone")}}}
	err := realignFeatureDirs(dst, []*features.Set{set})
	if err == nil || !strings.Contains(err.Error(), "stage feature x_0") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindContainerUsers(t *testing.T) {
	for name, tc := range map[string]struct {
		metadata             []imagemeta.Entry
		compose, image       string
		wantContainer, wantR string
	}{
		"image user":            {image: "img", wantContainer: "img", wantR: "img"},
		"compose over image":    {compose: "svc", image: "img", wantContainer: "svc", wantR: "svc"},
		"last metadata wins":    {metadata: []imagemeta.Entry{{ContainerUser: "a", RemoteUser: "ra"}, {ContainerUser: "b"}, {RemoteUser: "rc"}, {}}, compose: "svc", image: "img", wantContainer: "b", wantR: "rc"},
		"remote from container": {metadata: []imagemeta.Entry{{ContainerUser: "a"}}, image: "img", wantContainer: "a", wantR: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			c, r := findContainerUsers(tc.metadata, tc.compose, tc.image)
			if c != tc.wantContainer || r != tc.wantR {
				t.Errorf("users = %q/%q, want %q/%q", c, r, tc.wantContainer, tc.wantR)
			}
		})
	}
}

// fakeDockerAPI is a docker.API whose image, version and container calls answer
// from its fields; any other call panics through the nil embedded interface.
type fakeDockerAPI struct {
	docker.API
	version      string
	versionErr   error
	imageConfig  *dockerspec.DockerOCIImageConfig
	containers   []string
	listErr      error
	inspect      map[string]container.InspectResponse
	inspectErr   error
	stopErr      error
	stopped      []string
	stopTimeouts []int
}

func (f *fakeDockerAPI) ServerVersion(context.Context, mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error) {
	return mobyclient.ServerVersionResult{Version: f.version}, f.versionErr
}

func (f *fakeDockerAPI) ImageInspect(context.Context, string, ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
	return mobyclient.ImageInspectResult{InspectResponse: image.InspectResponse{Config: f.imageConfig}}, nil
}

func (f *fakeDockerAPI) ContainerList(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	var items []container.Summary
	for _, id := range f.containers {
		items = append(items, container.Summary{ID: id})
	}
	return mobyclient.ContainerListResult{Items: items}, f.listErr
}

func (f *fakeDockerAPI) ContainerInspect(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return mobyclient.ContainerInspectResult{Container: f.inspect[id]}, f.inspectErr
}

func (f *fakeDockerAPI) ContainerStop(_ context.Context, id string, opts mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
	f.stopped = append(f.stopped, id)
	if opts.Timeout != nil {
		f.stopTimeouts = append(f.stopTimeouts, *opts.Timeout)
	}
	return mobyclient.ContainerStopResult{}, f.stopErr
}

func TestEngineAtLeast23(t *testing.T) {
	if engineAtLeast23(t.Context(), nil) || engineAtLeast23(t.Context(), &docker.EngineClient{}) {
		t.Error("no engine must not count as >= 23")
	}
	if engineAtLeast23(t.Context(), &docker.EngineClient{API: &fakeDockerAPI{version: "25.0.0", versionErr: errors.New("down")}}) {
		t.Error("an unreachable daemon must not count as >= 23")
	}
	for version, want := range map[string]bool{"23.0.1": true, "24.0.7": true, "22.06.2": false, "20.10.7": false, "": false} {
		if got := engineAtLeast23(t.Context(), &docker.EngineClient{API: &fakeDockerAPI{version: version}}); got != want {
			t.Errorf("engineAtLeast23(%q) = %v, want %v", version, got, want)
		}
	}
}

// dockerCall is one recorded docker CLI invocation.
type dockerCall []string

// fakeDockerRunner is an exec.Runner standing in for the docker CLI: reply
// answers each invocation, and every invocation is recorded.
type fakeDockerRunner struct {
	calls []dockerCall
	reply func(args []string) (stdout string, code int, err error)
}

func (f *fakeDockerRunner) Run(_ context.Context, _ io.Writer, _ string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append(dockerCall{}, args...))
	if f.reply == nil {
		return nil, nil, 0, nil
	}
	out, code, err := f.reply(args)
	return []byte(out), []byte("stderr of " + strings.Join(args, " ")), code, err
}

func fakeDockerClient(r *fakeDockerRunner) *docker.Client {
	return &docker.Client{DockerPath: "docker", Log: log.Null, Runner: r}
}

func TestBuildFeatureContentImage(t *testing.T) {
	t.Run("builds the content image from the folder", func(t *testing.T) {
		folder := t.TempDir()
		r := &fakeDockerRunner{}
		if err := buildFeatureContentImage(t.Context(), fakeDockerClient(r), folder); err != nil {
			t.Fatal(err)
		}
		dockerfile := filepath.Join(folder, "Dockerfile.buildContent")
		want := dockerCall{"build", "-f", dockerfile, "-t", imagemeta.ComposeFeatureContentImage, folder}
		if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0], want) {
			t.Fatalf("calls = %v, want [%v]", r.calls, want)
		}
		data, err := os.ReadFile(dockerfile)
		if err != nil || !strings.Contains(string(data), "COPY . /tmp/build-features/") {
			t.Errorf("Dockerfile = %q, %v", data, err)
		}
	})
	for name, tc := range map[string]struct {
		folder  func(t *testing.T) string
		reply   func([]string) (string, int, error)
		wantErr string
	}{
		"folder missing":      {folder: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }, wantErr: "write feature content Dockerfile"},
		"docker fails to run": {reply: func([]string) (string, int, error) { return "", 0, errors.New("no docker") }, wantErr: "build feature content image: exec docker: no docker"},
		"build fails":         {reply: func([]string) (string, int, error) { return "", 2, nil }, wantErr: "build feature content image failed (exit 2): stderr of build"},
	} {
		t.Run(name, func(t *testing.T) {
			folder := t.TempDir()
			if tc.folder != nil {
				folder = tc.folder(t)
			}
			err := buildFeatureContentImage(t.Context(), fakeDockerClient(&fakeDockerRunner{reply: tc.reply}), folder)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// extendRun is one extendImageWithFeatures call over a local Feature with a
// fake docker CLI and engine.
type extendRun struct {
	useBuildx   bool
	imageNames  []string
	imageConfig *dockerspec.DockerOCIImageConfig
	reply       func([]string) (string, int, error)
	fbOpts      FeatureBuildOptions
	features    map[string]any
	engine      string // the daemon's version, 25.0.0 when empty
}

type extendResult struct {
	names    []string
	err      error
	calls    []dockerCall
	build    dockerCall // the extended image's build
	features string     // the features folder
}

func (e extendRun) run(t *testing.T) extendResult {
	t.Helper()
	base := t.TempDir()
	writeLocalFeature(t, base, "hello", nil)
	r := &fakeDockerRunner{reply: e.reply}
	opts := e.fbOpts
	opts.FeaturesBasePath = base
	cfg := e.features
	if cfg == nil {
		cfg = map[string]any{"./hello": map[string]any{}}
	}
	version := e.engine
	if version == "" {
		version = "25.0.0"
	}
	names, err := extendImageWithFeatures(t.Context(), log.Null, fakeDockerClient(r), &docker.EngineClient{API: &fakeDockerAPI{version: version, imageConfig: e.imageConfig}},
		"base:latest", cfg, e.useBuildx, e.imageNames, &opts)
	res := extendResult{names: names, err: err, calls: r.calls}
	for _, c := range r.calls {
		if slices.ContainsFunc(c, func(a string) bool { return strings.HasSuffix(a, "Dockerfile.extended") }) {
			res.build = c
			res.features = c[len(c)-1]
			t.Cleanup(func() { os.RemoveAll(res.features) })
		}
	}
	return res
}

// tagsOf lists the -t values of a build.
func tagsOf(c dockerCall) []string {
	var tags []string
	for i, a := range c {
		if a == "-t" && i+1 < len(c) {
			tags = append(tags, c[i+1])
		}
	}
	return tags
}

// The extended image is tagged with every given image name, else
// <base>-features, and the names it reports are those tags.
func TestExtendImageWithFeaturesTags(t *testing.T) {
	for name, tc := range map[string]struct {
		names, want []string
	}{
		"no names":  {nil, []string{"base:latest-features"}},
		"one name":  {[]string{"one"}, []string{"one"}},
		"two names": {[]string{"one", "two"}, []string{"one", "two"}},
	} {
		t.Run(name, func(t *testing.T) {
			res := extendRun{imageNames: tc.names}.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			if !reflect.DeepEqual(tagsOf(res.build), tc.want) || !reflect.DeepEqual(res.names, tc.want) {
				t.Errorf("tags = %v, names = %v, want %v", tagsOf(res.build), res.names, tc.want)
			}
		})
	}
}

// Without metadata naming a user, the Features install as the image's user,
// root when the image declares none.
func TestExtendImageWithFeaturesUsesTheImageUser(t *testing.T) {
	for name, tc := range map[string]struct {
		config *dockerspec.DockerOCIImageConfig
		want   string
	}{
		"no config": {nil, "root"},
		"no user":   {&dockerspec.DockerOCIImageConfig{}, "root"},
		"image's user": {func() *dockerspec.DockerOCIImageConfig {
			c := &dockerspec.DockerOCIImageConfig{}
			c.User = "app"
			return c
		}(), "app"},
	} {
		t.Run(name, func(t *testing.T) {
			res := extendRun{imageConfig: tc.config}.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			data, err := os.ReadFile(filepath.Join(res.features, imagemeta.FeaturesBuiltinEnvFile))
			if err != nil {
				t.Fatal(err)
			}
			if want := "_CONTAINER_USER=" + tc.want + "\n"; !strings.Contains(string(data), want) {
				t.Errorf("builtin env = %q, want %q", data, want)
			}
		})
	}
}

// The classic builder needs the Features' content image built first, and its
// failure stops the build.
func TestExtendImageWithFeaturesContentImage(t *testing.T) {
	res := extendRun{}.run(t)
	if res.err != nil {
		t.Fatal(res.err)
	}
	if len(res.calls) != 2 || !slices.Contains(res.calls[0], imagemeta.ComposeFeatureContentImage) {
		t.Fatalf("calls = %v, want the content image build then the extended build", res.calls)
	}
	failed := extendRun{reply: func(args []string) (string, int, error) {
		if slices.Contains(args, imagemeta.ComposeFeatureContentImage) {
			return "", 1, nil
		}
		return "", 0, nil
	}}.run(t)
	if failed.err == nil || !strings.Contains(failed.err.Error(), "build feature content image failed") || failed.build != nil {
		t.Fatalf("err = %v, extended build = %v; want the content image failure and no extended build", failed.err, failed.build)
	}
}

// A buildx builder that cannot see local images (docker-container or remote
// driver) is replaced by the context's docker-driver builder for the Features
// build; a docker-driver builder is kept.
func TestExtendImageWithFeaturesSwitchesToADockerDriverBuilder(t *testing.T) {
	for driver, want := range map[string]bool{"docker-container": true, "remote": true, "docker": false} {
		t.Run(driver, func(t *testing.T) {
			reply := func(args []string) (string, int, error) {
				switch strings.Join(args, " ") {
				case "buildx inspect":
					return "Name: active\nDriver: " + driver + "\n", 0, nil
				case "context inspect --format {{.Name}}":
					return "default\n", 0, nil
				case "buildx ls":
					return "NAME/NODE DRIVER/ENDPOINT\nactive " + driver + "\ndefault docker\n", 0, nil
				}
				return "", 0, nil
			}
			res := extendRun{useBuildx: true, reply: reply}.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			i := slices.Index(res.build, "--builder")
			if (i >= 0) != want || (want && res.build[i+1] != "default") {
				t.Errorf("build = %v, want --builder default: %v", res.build, want)
			}
		})
	}
}

// --experimental-lockfile writes the lockfile; --experimental-frozen-lockfile
// fails when the resolved Features differ from it. An existing lockfile pins
// the Features it records.
func TestExtendImageWithFeaturesLockfile(t *testing.T) {
	t.Run("written", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "devcontainer.json")
		res := extendRun{fbOpts: FeatureBuildOptions{Lockfile: true, ConfigPath: configPath}}.run(t)
		if res.err != nil {
			t.Fatal(res.err)
		}
		if _, err := os.Stat(features.LockfilePath(configPath)); err != nil {
			t.Errorf("lockfile not written: %v", err)
		}
	})
	t.Run("left alone without the flags", func(t *testing.T) {
		t.Chdir(t.TempDir())
		configPath := filepath.Join(t.TempDir(), "devcontainer.json")
		if err := os.WriteFile(features.LockfilePath(configPath), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := (extendRun{fbOpts: FeatureBuildOptions{ConfigPath: configPath}}).run(t); res.err != nil {
			t.Fatal(res.err)
		}
		if data, err := os.ReadFile(features.LockfilePath(configPath)); err != nil || string(data) != "{}" {
			t.Errorf("lockfile = %q, %v; want it untouched", data, err)
		}
		// Without a configuration there is no lockfile to write.
		if res := (extendRun{fbOpts: FeatureBuildOptions{Lockfile: true}}).run(t); res.err != nil {
			t.Fatal(res.err)
		}
		if entries, _ := os.ReadDir("."); len(entries) != 0 {
			t.Errorf("wrote %v in the working directory without a configuration", entries)
		}
	})
	t.Run("frozen drift", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "devcontainer.json")
		if err := os.WriteFile(features.LockfilePath(configPath), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := extendRun{fbOpts: FeatureBuildOptions{FrozenLockfile: true, ConfigPath: configPath}}.run(t)
		if res.err == nil || !strings.HasPrefix(res.err.Error(), "lockfile: ") || res.build != nil {
			t.Fatalf("err = %v, build = %v; want a lockfile error before any build", res.err, res.build)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		// TS readLockfile rethrows the JSON.parse error: the build fails with
		// its message before anything is built; --no-lockfile does not read it.
		configPath := filepath.Join(t.TempDir(), "devcontainer.json")
		if err := os.WriteFile(features.LockfilePath(configPath), []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := extendRun{fbOpts: FeatureBuildOptions{ConfigPath: configPath}}.run(t)
		lerr, ok := errors.AsType[*features.LockfileError](res.err)
		if !ok || lerr.Error() != `Unexpected token 'o', "not json" is not valid JSON` || res.build != nil {
			t.Fatalf("err = %v, build = %v; want the lockfile's SyntaxError before any build", res.err, res.build)
		}
		if unlocked := (extendRun{fbOpts: FeatureBuildOptions{ConfigPath: configPath, NoLockfile: true}}).run(t); unlocked.err != nil {
			t.Fatalf("--no-lockfile: err = %v", unlocked.err)
		}
	})
	t.Run("pins on read", func(t *testing.T) {
		uri := serveBytes(t, featureTgzBytes(t, map[string]string{"devcontainer-feature.json": `{"id":"tb"}`}), http.StatusOK) + "/devcontainer-feature-tb.tgz"
		configPath := filepath.Join(t.TempDir(), "devcontainer.json")
		lock := fmt.Sprintf(`{"features":{%q:{"version":"1","resolved":%q,"integrity":"sha256:%s"}}}`, uri, uri, strings.Repeat("0", 64))
		if err := os.WriteFile(features.LockfilePath(configPath), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
		res := extendRun{features: map[string]any{uri: true}, fbOpts: FeatureBuildOptions{ConfigPath: configPath}}.run(t)
		if _, ok := errors.AsType[*digestMismatchError](res.err); !ok {
			t.Fatalf("err = %v, want the lockfile's digest mismatch", res.err)
		}
		// --no-lockfile neither reads nor verifies it.
		unlocked := extendRun{features: map[string]any{uri: true}, fbOpts: FeatureBuildOptions{ConfigPath: configPath, NoLockfile: true}}.run(t)
		if unlocked.err != nil {
			t.Fatalf("--no-lockfile: err = %v", unlocked.err)
		}
	})
}

// The build of the extended image gets a DOCKER_CONFIG with the CLI's
// credentials for the registry it is tagged for.
func TestExtendImageWithFeaturesBridgesRegistryAuth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a shell script")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "docker.log")
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$* DOCKER_CONFIG=$DOCKER_CONFIG\" >> \"$FAKE_DOCKER_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ambient := t.TempDir()
	t.Setenv("DOCKER_CONFIG", ambient)
	t.Setenv("DEVCONTAINERS_OCI_AUTH", "registry.example.com|user|secret")

	base := t.TempDir()
	writeLocalFeature(t, base, "hello", nil)
	client := &docker.Client{DockerPath: script, Env: []string{"FAKE_DOCKER_LOG=" + logPath}, Log: log.Null}
	_, err := extendImageWithFeatures(t.Context(), log.Null, client, &docker.EngineClient{API: &fakeDockerAPI{}}, "base:latest",
		map[string]any{"./hello": true}, false, []string{"registry.example.com/app:1"}, &FeatureBuildOptions{FeaturesBasePath: base})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var build string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, "Dockerfile.extended") {
			build = line
			fields := strings.Fields(line)
			t.Cleanup(func() { os.RemoveAll(fields[len(fields)-2]) })
		}
	}
	_, cfg, _ := strings.Cut(build, "DOCKER_CONFIG=")
	if cfg == "" || cfg == ambient || !strings.Contains(filepath.Base(cfg), "devcontainer-buildauth-") {
		t.Errorf("extended build ran with DOCKER_CONFIG=%q, want a temporary build-auth config (log: %s)", cfg, data)
	}
}

// With buildx, only a daemon older than 23 needs the dockerfile:1.4 syntax
// directive for the build contexts; the classic builder never gets one.
func TestExtendImageWithFeaturesSyntaxDirective(t *testing.T) {
	for name, tc := range map[string]struct {
		run  extendRun
		want bool
	}{
		"buildx, engine 25":  {extendRun{useBuildx: true}, false},
		"buildx, engine 20":  {extendRun{useBuildx: true, engine: "20.10.7"}, true},
		"classic, engine 20": {extendRun{engine: "20.10.7"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			res := tc.run.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			data, err := os.ReadFile(filepath.Join(res.features, "Dockerfile.extended"))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(data), "docker/dockerfile:1.4"); got != tc.want {
				t.Errorf("syntax directive present = %v, want %v:\n%s", got, tc.want, data)
			}
		})
	}
}

// A Feature's metadata entry falls back to the Feature's id when its source
// has no user reference.
func TestFeatureMetadataEntryFallsBackToTheFeatureID(t *testing.T) {
	for name, src := range map[string]features.SourceInfo{
		"no source":      nil,
		"empty user ref": &features.LocalSource{},
	} {
		set := &features.Set{SourceInfo: src, Features: []features.Feature{{ID: "hello"}}}
		if got := featureMetadataEntry(set, false).ID; got != "hello" {
			t.Errorf("%s: id = %q, want hello", name, got)
		}
	}
}

// Sets without a staged Feature are left as they are.
func TestRealignFeatureDirsSkipsUnstagedSets(t *testing.T) {
	dst := t.TempDir()
	unstaged := &features.Set{Features: []features.Feature{{ID: "x"}}}
	if err := realignFeatureDirs(dst, []*features.Set{{}, unstaged}); err != nil {
		t.Fatal(err)
	}
	if f := unstaged.Features[0]; f.ConsecutiveId != "" || f.CachePath != "" {
		t.Errorf("unstaged Feature = %+v, want it untouched", f)
	}
}
