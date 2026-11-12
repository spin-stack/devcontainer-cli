package devtools

import (
	"os"
	"testing"

	"github.com/spin-stack/go-tools/versions/gate"
)

func TestVersionsYAMLIsTheOnlyPin(t *testing.T) {
	// Excused: reference/ is upstream's submodule, pinned by its own lockfile;
	// src/test/ is upstream's fixtures, whose lockfiles' digests are what the
	// parity tests compare; updateUID.Dockerfile is upstream's, and its
	// BASE_IMAGE is the user's image handed in at run time, not a pin;
	// internal/imagemeta/testdata/ holds Dockerfiles the TS CLI generated; the
	// testdata/ts-oracle goldens hold digests as inputs of reference functions.
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
