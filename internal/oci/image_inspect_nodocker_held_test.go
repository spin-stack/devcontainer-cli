package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A Docker Hub image is read from registry-1.docker.io with the credentials
// stored for docker.io, the registry as named: TS inspectImageInRegistry
// (utils.ts) authenticates with ref.registry while requesting the API host.
func TestInspectImageInRegistryDockerHubUsesDockerIOCredentials(t *testing.T) {
	reg := newImageRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "hubuser" || pass != "hubtoken" {
			w.Header().Set("WWW-Authenticate", `Basic realm="hub"`)
			http.Error(w, `{"errors":[{"code":"UNAUTHORIZED"}]}`, http.StatusUnauthorized)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	reg.addImage(t, "library/node", "18", ocispec.MediaTypeImageManifest, ImageConfig{User: "node"})

	c := testImageClient()
	c.env["DEVCONTAINERS_OCI_AUTH"] = "docker.io|hubuser|hubtoken"
	c.retryClient = &http.Client{Transport: originKeeper{hostRewriter{target}}}
	got, err := c.InspectImageInRegistry(context.Background(), "node:18", ocispec.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatalf("InspectImageInRegistry: %v", err)
	}
	if got.Config.User != "node" {
		t.Fatalf("user = %q, want node", got.Config.User)
	}
}

// originKeeper answers with the request as it was addressed, so the auth
// client sees the challenge come from the registry it asked, not the test
// server the request was rewritten to.
type originKeeper struct{ next http.RoundTripper }

func (o originKeeper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := o.next.RoundTrip(req)
	if resp != nil {
		resp.Request = req
	}
	return resp, err
}

// An index entry is chosen as TS getImageIndexEntryForPlatform
// (containerCollectionsOCI.ts) finds it: the first entry for the os and
// architecture when no variant is asked for, else the first that also has the
// variant.
func TestInspectImageInRegistryIndexVariant(t *testing.T) {
	reg := newImageRegistry()
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := localhostRegistry(t, srv)
	v6 := reg.addImage(t, "org/img", "", ocispec.MediaTypeImageManifest, ImageConfig{User: "v6"})
	v7 := reg.addImage(t, "org/img", "", ocispec.MediaTypeImageManifest, ImageConfig{User: "v7"})
	v6.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm", Variant: "v6"}
	v7.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm", Variant: "v7"}
	body, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{v6, v7}})
	if err != nil {
		t.Fatal(err)
	}
	reg.manifests["org/img/latest"] = servedManifest{ocispec.MediaTypeImageIndex, body}

	for variant, want := range map[string]string{"": "v6", "v6": "v6", "v7": "v7"} {
		got, err := testImageClient().InspectImageInRegistry(context.Background(), host+"/org/img", ocispec.Platform{OS: "linux", Architecture: "arm", Variant: variant})
		if err != nil {
			t.Fatalf("variant %q: %v", variant, err)
		}
		if got.Config.User != want {
			t.Errorf("variant %q: user = %q, want %q", variant, got.Config.User, want)
		}
	}
}
