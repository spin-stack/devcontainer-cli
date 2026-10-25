package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
)

// A fetched feature with an active advisory gets TS's FEATURE ADVISORIES
// warning in the log; one without gets nothing. Go had the advisory matcher but
// never called it, so the warning TS writes on every features config was lost.
func TestWriteFeatureAdvisoriesWarnsOnTheLog(t *testing.T) {
	manifest := &features.ControlManifest{FeatureAdvisories: []features.Advisory{{
		FeatureID: "ghcr.io/o/f/a", IntroducedInVersion: "1.0.0", FixedInVersion: "1.2.0", Description: "a broke",
	}}}
	set := func(version string) []*features.Set {
		return []*features.Set{{
			SourceInfo: &features.OCISource{Registry: "ghcr.io", Namespace: "o/f", ID: "a"},
			Features:   []features.Feature{{Version: version}},
		}}
	}
	var buf bytes.Buffer
	writeFeatureAdvisories(log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: &buf}), manifest, set("1.1.0"))
	if out := buf.String(); !strings.Contains(out, "FEATURE ADVISORIES:\n- ghcr.io/o/f/a:1.1.0:\n  - a broke (introduced in 1.0.0, fixed in 1.2.0)") {
		t.Errorf("log lacks the advisory warning:\n%s", out)
	}
	buf.Reset()
	writeFeatureAdvisories(log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: &buf}), manifest, set("1.2.0"))
	if buf.Len() != 0 {
		t.Errorf("fixed version logged %q", buf.String())
	}
}
