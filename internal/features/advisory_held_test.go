package features

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// TS sanitizeControlManifest (reference/src/spec-configuration/
// controlManifest.ts) keeps each entry whose fields are strings, an empty one
// included, and drops only the entries that are not: a mistyped, missing or
// null field, or an entry that is no object, never takes its siblings along.
// The want is what node prints for the TS function on the same body.
func TestGetControlManifestKeepsEveryEntryWithStringFields(t *testing.T) {
	body := `{"disallowedFeatures":[{"featureIdPrefix":""},{"featureIdPrefix":1},{"documentationURL":"u"},"x",` +
		`{"featureIdPrefix":"p","documentationURL":"https://d"}],` +
		`"featureAdvisories":[{"featureId":"ghcr.io/o/f","introducedInVersion":"1.0.0","fixedInVersion":"1.1.0","description":""},` +
		`{"featureId":1,"introducedInVersion":"1.0.0","fixedInVersion":"1.1.0","description":"d"},` +
		`{"introducedInVersion":"1.0.0","fixedInVersion":"1.1.0","description":"d"},` +
		`{"featureId":"a","fixedInVersion":"1.1.0","description":"d"},` +
		`{"featureId":"a","introducedInVersion":"1.0.0","description":"d"},` +
		`{"featureId":"a","introducedInVersion":"1.0.0","fixedInVersion":"1.1.0"},` +
		`{"featureId":"a","introducedInVersion":null,"fixedInVersion":"1.1.0","description":"d"},7]}`
	client := &http.Client{Transport: respond(200, body)}
	m := GetControlManifest(t.Context(), t.TempDir(), client, log.Null)
	advisory := Advisory{FeatureID: "ghcr.io/o/f", IntroducedInVersion: "1.0.0", FixedInVersion: "1.1.0"}
	want := &ControlManifest{
		DisallowedFeatures: []DisallowedFeature{{}, {FeatureIDPrefix: "p", DocumentationURL: "https://d"}},
		FeatureAdvisories:  []Advisory{advisory},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("manifest = %+v\nwant %+v", m, want)
	}
	// The advisory without a description still applies to the Feature.
	set := &Set{SourceInfo: &OCISource{Registry: "ghcr.io", Namespace: "o", ID: "f"}, Features: []Feature{{ID: "f", Version: "1.0.5"}}}
	got := FeatureAdvisories(m, []*Set{set}, func(w string) { t.Errorf("warned: %s", w) })
	if wantList := []WithAdvisory{{FeatureID: "ghcr.io/o/f", Version: "1.0.5", Advisories: []Advisory{advisory}}}; !reflect.DeepEqual(got, wantList) {
		t.Fatalf("advisories = %+v, want %+v", got, wantList)
	}
}
