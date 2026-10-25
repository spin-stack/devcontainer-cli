package cli

import (
	"bytes"
	"strings"
	"testing"
)

// info <mode> <feature> takes the mode from its first argument and the
// Feature from its second.
func TestFeaturesInfoCommandArguments(t *testing.T) {
	reg := newReadRegistry(t)
	reg.manifests["ns/tool"] = featureManifest("tool")
	reg.tags["ns/tool"] = []string{"1.0.0"}

	cmd := realFeaturesInfoCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"tags", reg.host + "/ns/tool:1", "--output-format", "json"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(out.String()), ""); got != `{"publishedTags":["1.0.0"]}` {
		t.Errorf("stdout = %s, want the Feature's tags", out.String())
	}
}
