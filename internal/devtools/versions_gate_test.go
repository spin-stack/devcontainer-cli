package devtools

import (
	"os"
	"testing"

	"github.com/spin-stack/go-tools/versions/gate"
)

// versions.yaml is the one place this repository pins an image, a download or a
// tool version: a pin written anywhere else, an entry nothing reads, or one a
// bump could not rewrite fails. Excused: reference/ is upstream's submodule,
// pinned by its own lockfile; src/test/ is upstream's fixtures, whose lockfiles'
// digests are what the parity tests compare; updateUID.Dockerfile is upstream's,
// shipped in the binary, and its BASE_IMAGE is the user's image, handed in at run
// time (docker.BuildUIDImage), not a pin; internal/imagemeta/testdata/ holds
// Dockerfiles the TS CLI generated, whose ARG defaults are its output, not pins;
// the testdata/ts-oracle goldens hold digests as inputs of reference functions.
func TestVersionsYAMLIsTheOnlyPin(t *testing.T) {
	g := gate.Gate{Root: "../..", Elsewhere: []string{
		"go.sum", "reference/", "src/test/", "internal/docker/updateUID.Dockerfile", "internal/imagemeta/testdata/",
	}}
	goldens, err := TSOracleGoldens(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	g.Elsewhere = append(g.Elsewhere, goldens...)
	if err := g.Check(); err != nil {
		t.Error(err)
	}
}
