package cli

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// templateManifest is a Template manifest whose annotations are the given
// JSON object body.
func templateManifest(annotations string) []byte {
	return []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.devcontainers","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},` +
		`"layers":[],"annotations":{` + annotations + `}}`)
}

func runTemplatesMetadataCmd(t *testing.T, templateID string) (string, error) {
	t.Helper()
	cmd := realTemplatesMetadataCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{templateID})
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

// templates metadata <id> prints the manifest's metadata annotation as
// JSON.stringify(JSON.parse(annotation)) does: compacted, keys in source order.
func TestTemplatesMetadataPrintsTheAnnotation(t *testing.T) {
	reg := newReadRegistry(t)
	metadata := `{ "id": "tpl", "version": "1.0.0", "a": [1, 2] }`
	reg.manifests["ns/tpl"] = templateManifest(`"dev.containers.metadata":` + strconv.Quote(metadata))

	got, err := runTemplatesMetadataCmd(t, reg.host+"/ns/tpl:latest")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"tpl","version":"1.0.0","a":[1,2]}` + "\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// Every failure prints an empty object on stdout, as TS does before exiting 1,
// and returns the reason.
func TestTemplatesMetadataFailures(t *testing.T) {
	reg := newReadRegistry(t)
	reg.manifests["ns/none"] = templateManifest(`"other":"x"`)
	reg.manifests["ns/empty"] = templateManifest(`"dev.containers.metadata":""`)
	reg.manifests["ns/broken"] = templateManifest(`"dev.containers.metadata":"{\"id\":"`)
	cases := []struct{ name, id, want string }{
		{"unparsable id", "UPPER/Not A Ref", "parse template identifier"},
		{"no manifest", reg.host + "/ns/missing:latest", "fetch manifest for template"},
		{"no annotation", reg.host + "/ns/none:latest", "does not contain metadata"},
		{"empty annotation", reg.host + "/ns/empty:latest", "does not contain metadata"},
		{"invalid annotation", reg.host + "/ns/broken:latest", "has invalid metadata"},
	}
	for _, c := range cases {
		got, err := runTemplatesMetadataCmd(t, c.id)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if got != "{}\n" {
			t.Errorf("%s: stdout = %q, want an empty object", c.name, got)
		}
	}
}
