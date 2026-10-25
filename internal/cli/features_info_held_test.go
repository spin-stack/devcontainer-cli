package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readRegistry is a read-only OCI distribution registry over plain HTTP on
// 127.0.0.1 (which the OCI client addresses without TLS): it serves the tag
// lists and manifests a test puts in it and 404s anything else.
type readRegistry struct {
	host      string
	tags      map[string][]string // repository -> tags
	manifests map[string][]byte   // repository -> manifest served for any reference
}

func newReadRegistry(t *testing.T) *readRegistry {
	t.Helper()
	r := &readRegistry{tags: map[string][]string{}, manifests: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

func (r *readRegistry) serve(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/v2/")
	if repo, ok := strings.CutSuffix(path, "/tags/list"); ok {
		if tags, ok := r.tags[repo]; ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
			return
		}
	}
	if i := strings.Index(path, "/manifests/"); i >= 0 {
		if body, ok := r.manifests[path[:i]]; ok {
			sum := sha256.Sum256(body)
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			if req.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
			return
		}
	}
	if path == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"not found"}]}`))
}

// featureManifest is a Feature manifest whose keys are deliberately not in
// sorted order, so a re-marshal would show.
func featureManifest(id string) []byte {
	return []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.devcontainers","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},` +
		`"layers":[{"mediaType":"application/vnd.devcontainers.layer.v1+tar","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2}],` +
		`"annotations":{"dev.containers.metadata":"{\"id\":\"` + id + `\",\"version\":\"1.0.0\",\"name\":\"T\"}","com.github.package.type":"devcontainer_feature"}}`)
}

func runInfo(t *testing.T, mode, id, format string) (string, error) {
	t.Helper()
	out := &captureOutput{}
	err := runFeaturesInfo(t.Context(), out, mode, id, "info", format)
	return out.out.String(), err
}

func TestFeaturesInfoModes(t *testing.T) {
	reg := newReadRegistry(t)
	manifest := featureManifest("tool")
	reg.manifests["ns/tool"] = manifest
	reg.tags["ns/tool"] = []string{"1.0.0"}
	id := reg.host + "/ns/tool:1"

	var indented bytes.Buffer
	if err := json.Indent(&indented, manifest, "", "  "); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifest)
	canonical := reg.host + "/ns/tool@sha256:" + hex.EncodeToString(sum[:])
	manifestText := encloseStringInBox("Manifest") + "\n" + indented.String() + "\n\n" +
		encloseStringInBox("Canonical Identifier") + "\n" + canonical + "\n\n"
	tagsText := encloseStringInBox("Published Tags") + "\n1.0.0\n"
	depsBox := encloseStringInBox("Dependency Tree (Render with https://mermaid.live/)")

	got, err := runInfo(t, "manifest", id, "text")
	if err != nil || got != manifestText {
		t.Errorf("manifest text: err = %v\n%s\nwant:\n%s", err, got, manifestText)
	}
	got, err = runInfo(t, "tags", id, "text")
	if err != nil || got != tagsText {
		t.Errorf("tags text: err = %v\n%q\nwant %q", err, got, tagsText)
	}
	got, err = runInfo(t, "dependencies", id, "text")
	if err != nil || !strings.HasPrefix(got, depsBox+"\nflowchart") {
		t.Errorf("dependencies text: err = %v\n%s", err, got)
	}
	got, err = runInfo(t, "verbose", id, "text")
	if err != nil || !strings.HasPrefix(got, manifestText+tagsText+depsBox+"\n") {
		t.Errorf("verbose text: err = %v\n%s", err, got)
	}

	var doc map[string]json.RawMessage
	got, err = runInfo(t, "manifest", id, "json")
	if err != nil || json.Unmarshal([]byte(got), &doc) != nil || len(doc) != 2 || string(doc["canonicalId"]) != `"`+canonical+`"` || doc["manifest"] == nil {
		t.Errorf("manifest json: err = %v\n%s", err, got)
	}
	got, err = runInfo(t, "tags", id, "json")
	if err != nil || strings.Join(strings.Fields(got), "") != `{"publishedTags":["1.0.0"]}` {
		t.Errorf("tags json: err = %v\n%s", err, got)
	}
	got, err = runInfo(t, "dependencies", id, "json")
	if err != nil || got != "{}\n" {
		t.Errorf("dependencies json: err = %v\n%q, want an empty object", err, got)
	}
	got, err = runInfo(t, "verbose", id, "json")
	doc = nil
	if err != nil || json.Unmarshal([]byte(got), &doc) != nil || len(doc) != 3 {
		t.Errorf("verbose json: err = %v\n%s", err, got)
	}
}

// Every failure exits non-zero; under --output-format json stdout still holds
// an empty object, and under text it holds nothing.
func TestFeaturesInfoFailures(t *testing.T) {
	reg := newReadRegistry(t)
	reg.manifests["ns/untagged"] = featureManifest("untagged")
	reg.tags["ns/untagged"] = []string{}
	cases := []struct {
		name, mode, id, want string
	}{
		{"unparsable id", "manifest", "not a feature", "Failed to parse Feature identifier"},
		{"no manifest", "manifest", reg.host + "/ns/missing:1", "No manifest found"},
		{"no tags", "tags", reg.host + "/ns/untagged:1", "No published versions found"},
		{"unknown repository", "tags", reg.host + "/ns/missing:1", "No published versions found"},
	}
	for _, c := range cases {
		for format, stdout := range map[string]string{"json": "{}\n", "text": ""} {
			got, err := runInfo(t, c.mode, c.id, format)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s (%s): err = %v, want %q", c.name, format, err, c.want)
			}
			if got != stdout {
				t.Errorf("%s (%s): stdout = %q, want %q", c.name, format, got, stdout)
			}
		}
	}
	if err := runFeaturesInfo(t.Context(), &captureOutput{}, "tags", reg.host+"/ns/untagged:1", "loud", "text"); err == nil || !strings.Contains(err.Error(), "log-level") {
		t.Errorf("invalid log level: err = %v", err)
	}
}
