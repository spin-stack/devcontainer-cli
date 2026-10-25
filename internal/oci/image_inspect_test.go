package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/log"
)

// imageRegistry is a read-only httptest registry serving manifests (by tag or
// digest) and blobs, recording the host and path of every request.
type imageRegistry struct {
	mu        sync.Mutex
	manifests map[string]servedManifest // "<repo>/<reference>"
	blobs     map[string][]byte         // "<repo>/<digest>"
	requests  []string                  // "<host><path>"
}

type servedManifest struct {
	mediaType string
	body      []byte
}

func newImageRegistry() *imageRegistry {
	return &imageRegistry{manifests: map[string]servedManifest{}, blobs: map[string][]byte{}}
}

func (r *imageRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.Host+req.URL.Path)
	path := strings.TrimPrefix(req.URL.Path, "/v2/")
	if repo, ref, ok := strings.Cut(path, "/manifests/"); ok {
		m, found := r.manifests[repo+"/"+ref]
		if !found {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", m.mediaType)
		w.Header().Set("Docker-Content-Digest", godigest.FromBytes(m.body).String())
		w.Header().Set("Content-Length", strconv.Itoa(len(m.body)))
		if req.Method != http.MethodHead {
			w.Write(m.body)
		}
		return
	}
	if repo, dgst, ok := strings.Cut(path, "/blobs/"); ok {
		b, found := r.blobs[repo+"/"+dgst]
		if !found {
			http.Error(w, `{"errors":[{"code":"BLOB_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if req.Method != http.MethodHead {
			w.Write(b)
		}
		return
	}
	http.NotFound(w, req)
}

// addImage stores a config blob and an image manifest for it, returning the
// manifest descriptor. tag may be empty (manifest reachable by digest only).
func (r *imageRegistry) addImage(t *testing.T, repo, tag, manifestType string, cfg ImageConfig) ocispec.Descriptor {
	t.Helper()
	blob, err := json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "config": cfg})
	if err != nil {
		t.Fatal(err)
	}
	cfgDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: godigest.FromBytes(blob), Size: int64(len(blob))}
	r.blobs[repo+"/"+cfgDesc.Digest.String()] = blob
	manifest, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": manifestType, "config": cfgDesc, "layers": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{MediaType: manifestType, Digest: godigest.FromBytes(manifest), Size: int64(len(manifest))}
	r.manifests[repo+"/"+desc.Digest.String()] = servedManifest{manifestType, manifest}
	if tag != "" {
		r.manifests[repo+"/"+tag] = servedManifest{manifestType, manifest}
	}
	return desc
}

func (r *imageRegistry) addIndex(t *testing.T, repo, tag, indexType string, entries map[string]ocispec.Descriptor) {
	t.Helper()
	var manifests []ocispec.Descriptor
	for plat, d := range entries {
		parts := strings.Split(plat, "/")
		p := &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
		if len(parts) > 2 {
			p.Variant = parts[2]
		}
		d.Platform = p
		manifests = append(manifests, d)
	}
	body, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": indexType, "manifests": manifests})
	if err != nil {
		t.Fatal(err)
	}
	r.manifests[repo+"/"+tag] = servedManifest{indexType, body}
}

func testImageClient() *Client {
	return &Client{log: log.Null, env: map[string]string{"DOCKER_CONFIG": "/nonexistent"}}
}

func TestInspectImageInRegistryManifest(t *testing.T) {
	reg := newImageRegistry()
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	want := ImageConfig{User: "node", Env: []string{"PATH=/usr/bin"}, Labels: map[string]string{"devcontainer.metadata": `[{"remoteUser":"node"}]`}}
	reg.addImage(t, "devcontainers/base", "1", "application/vnd.docker.distribution.manifest.v2+json", want)

	got, err := testImageClient().InspectImageInRegistry(context.Background(), host+"/devcontainers/base:1", ocispec.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatalf("InspectImageInRegistry: %v", err)
	}
	if got.Config.User != "node" || got.Config.Labels["devcontainer.metadata"] != want.Labels["devcontainer.metadata"] || len(got.Config.Env) != 1 {
		t.Fatalf("config = %+v, want %+v", got.Config, want)
	}
	if !strings.HasPrefix(got.ID, "sha256:") || got.OS != "linux" || got.Architecture != "amd64" {
		t.Fatalf("details = %+v", got)
	}
}

func TestInspectImageInRegistryIndexSelectsPlatform(t *testing.T) {
	for _, indexType := range []string{ocispec.MediaTypeImageIndex, dockerManifestListMediaType} {
		t.Run(indexType, func(t *testing.T) {
			reg := newImageRegistry()
			srv := httptest.NewServer(reg)
			defer srv.Close()
			host := strings.TrimPrefix(srv.URL, "http://")
			amd := reg.addImage(t, "org/img", "", ocispec.MediaTypeImageManifest, ImageConfig{User: "amd"})
			arm := reg.addImage(t, "org/img", "", ocispec.MediaTypeImageManifest, ImageConfig{User: "arm"})
			armV7 := reg.addImage(t, "org/img", "", ocispec.MediaTypeImageManifest, ImageConfig{User: "armv7"})
			reg.addIndex(t, "org/img", "latest", indexType, map[string]ocispec.Descriptor{"linux/amd64": amd, "linux/arm64": arm, "linux/arm/v7": armV7})

			c := testImageClient()
			for plat, wantUser := range map[string]string{"linux/amd64": "amd", "linux/arm64": "arm", "linux/arm/v7": "armv7"} {
				parts := strings.Split(plat, "/")
				p := ocispec.Platform{OS: parts[0], Architecture: parts[1]}
				if len(parts) > 2 {
					p.Variant = parts[2]
				}
				got, err := c.InspectImageInRegistry(context.Background(), host+"/org/img", p)
				if err != nil {
					t.Fatalf("%s: %v", plat, err)
				}
				if got.Config.User != wantUser {
					t.Errorf("%s: user = %q, want %q", plat, got.Config.User, wantUser)
				}
			}

			_, err := c.InspectImageInRegistry(context.Background(), host+"/org/img:latest", ocispec.Platform{OS: "windows", Architecture: "amd64"})
			if err == nil || err.Error() != "No manifest found for "+host+"/org/img:latest." {
				t.Fatalf("no matching platform: err = %v", err)
			}
		})
	}
}

func TestInspectImageInRegistryMissingManifest(t *testing.T) {
	reg := newImageRegistry()
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	_, err := testImageClient().InspectImageInRegistry(context.Background(), host+"/org/missing:1", ocispec.Platform{OS: "linux", Architecture: "amd64"})
	if err == nil || err.Error() != "No manifest found for "+host+"/org/missing:1." {
		t.Fatalf("err = %v", err)
	}
}

// hostRewriter sends every request to the test server, keeping the original
// host in the Host header so the registry sees which host was addressed.
type hostRewriter struct{ target *url.URL }

func (h hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Host = req.URL.Host
	r.URL.Scheme = h.target.Scheme
	r.URL.Host = h.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestInspectImageInRegistryDockerHub(t *testing.T) {
	reg := newImageRegistry()
	srv := httptest.NewServer(reg)
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	reg.addImage(t, "library/node", "18", ocispec.MediaTypeImageManifest, ImageConfig{User: "node"})
	reg.addImage(t, "someuser/tool", "latest", ocispec.MediaTypeImageManifest, ImageConfig{User: "tool"})

	c := testImageClient()
	c.retryClient = &http.Client{Transport: hostRewriter{target}}
	for name, wantUser := range map[string]string{"node:18": "node", "docker.io/node:18": "node", "someuser/tool": "tool"} {
		got, err := c.InspectImageInRegistry(context.Background(), name, ocispec.Platform{OS: "linux", Architecture: "amd64"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Config.User != wantUser {
			t.Errorf("%s: user = %q, want %q", name, got.Config.User, wantUser)
		}
	}
	for _, r := range reg.requests {
		if !strings.HasPrefix(r, "registry-1.docker.io/v2/") {
			t.Errorf("request to %q, want registry-1.docker.io", r)
		}
	}
	if len(reg.requests) == 0 {
		t.Fatal("no registry requests")
	}
}

func TestQualifyImageName(t *testing.T) {
	for in, want := range map[string]string{
		"node":                                   "docker.io/library/node",
		"node:18":                                "docker.io/library/node:18",
		"docker.io/node":                         "docker.io/library/node",
		"user/img:1":                             "docker.io/user/img:1",
		"mcr.microsoft.com/devcontainers/base:1": "mcr.microsoft.com/devcontainers/base:1",
		"localhost:5000/img":                     "docker.io/localhost:5000/img",
	} {
		if got := QualifyImageName(in); got != want {
			t.Errorf("QualifyImageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInspectImageInRegistryUnparsableName(t *testing.T) {
	_, err := testImageClient().InspectImageInRegistry(context.Background(), "Bad Name", ocispec.Platform{OS: "linux", Architecture: "amd64"})
	if err == nil || err.Error() != "Could not parse image name 'Bad Name'" {
		t.Fatalf("err = %v", err)
	}
}
