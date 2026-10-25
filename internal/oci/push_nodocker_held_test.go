package oci

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// The major.minor tag follows the highest published version with the same
// major and the same minor (TS getSemanticTags' `${major}.${minor}.x` range,
// publishCommandImpl.ts): neither a published 2.3.5 nor a published 1.4.0
// holds back 1.3.
func TestSemanticTagsMajorMinorRangeIsBothParts(t *testing.T) {
	for _, tt := range []struct {
		published []string
		want      []string
	}{
		{[]string{"2.3.5"}, []string{"1", "1.3", "1.3.0"}},
		{[]string{"1.4.0"}, []string{"1.3", "1.3.0"}},
	} {
		tags, skip, err := SemanticTags("1.3.0", tt.published)
		if err != nil || skip {
			t.Fatalf("SemanticTags(%v): skip=%v err=%v", tt.published, skip, err)
		}
		if !slices.Equal(tags, tt.want) {
			t.Errorf("published %v: tags = %v, want %v", tt.published, tags, tt.want)
		}
	}
}

// The pushed manifest is the one TS calculateManifestAndContentDigest
// (containerCollectionsOCIPush.ts) builds: schemaVersion 2, and a layer titled
// devcontainer-<collectionType>-<id>.tgz.
func TestPushArtifactManifest(t *testing.T) {
	reg := newFakeOCIRegistry()
	registry := reg.start(t)
	ref, err := ParseRef(registry + "/ns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(log.Null, map[string]string{"DOCKER_CONFIG": t.TempDir()})
	if _, err := client.PushArtifact(t.Context(), ref, writeTarball(t, "layer"), []string{"1.0.0"}, "template", nil); err != nil {
		t.Fatal(err)
	}
	reg.mu.Lock()
	body := reg.manifests["1.0.0"]
	reg.mu.Unlock()
	var m struct {
		SchemaVersion int `json:"schemaVersion"`
		Layers        []struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("manifest %q: %v", body, err)
	}
	if m.SchemaVersion != 2 || len(m.Layers) != 1 || m.Layers[0].Annotations["org.opencontainers.image.title"] != "devcontainer-template-hello.tgz" {
		t.Fatalf("manifest = %s", body)
	}
}
