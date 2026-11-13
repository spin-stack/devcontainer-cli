package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfigurationReportsTheFirstFeatureInDeclarationOrder(t *testing.T) {
	legacy9 := "Legacy feature '9' not supported."
	refusedZeta := "ERR: Feature '../zeta' could not be processed."
	for name, tc := range map[string]struct {
		features, override, additional string
		want                           string
	}{
		"declaration order":                {features: `{"../zeta":{},"../alpha":{}}`, want: refusedZeta},
		"integer-like ids first":           {features: `{"../zeta":{},"10":{},"9":{}}`, want: legacy9},
		"override adds a Feature after":    {features: `{"../zeta":{}}`, override: `{"features":{"../alpha":{}}}`, want: refusedZeta},
		"override integer-like id first":   {features: `{"../zeta":{}}`, override: `{"features":{"9":{}}}`, want: legacy9},
		"additional Features after config": {features: `{"../zeta":{}}`, additional: `{"9":{},"../alpha":{}}`, want: refusedZeta},
		"additional integer-like id first": {features: `{"../zeta":{}}`, additional: `{"../alpha":{},"10":{},"9":{}}`, want: refusedZeta},
	} {
		t.Run(name, func(t *testing.T) {
			startFakeEngine(t, nil)
			files := map[string]string{".devcontainer/devcontainer.json": `{"image":"alpine:3","features":` + tc.features + `}`}
			if tc.override != "" {
				files["override.json"] = tc.override
			}
			ws := writeConfigFixture(t, files)
			args := []string{"--workspace-folder", ws, "--include-features-configuration"}
			if tc.override != "" {
				args = append(args, "--override-config", filepath.Join(ws, "override.json"))
			}
			if tc.additional != "" {
				args = append(args, "--additional-features", tc.additional)
			}
			stdout, err := readConfigurationStdout(args...)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) || stdout != "" {
				t.Fatalf("stdout = %q, err = %v; want %q", stdout, err, tc.want)
			}
		})
	}
}

func TestReadConfigurationReportsAnAdditionalIntegerLikeIDAfterTheConfigs(t *testing.T) {
	startFakeEngine(t, nil)
	ws := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image":"alpine:3","features":{"zz":{}}}`})
	_, err := readConfigurationStdout("--workspace-folder", ws, "--include-features-configuration", "--additional-features", `{"9":{}}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Legacy feature 'zz' not supported.") {
		t.Fatalf("err = %v; want the config's legacy Feature zz first", err)
	}
}

func TestFeaturesResolveDependenciesListsFeaturesInDeclarationOrder(t *testing.T) {
	ids := []string{"zeta", "alpha", "mid", "beta", "omega"}
	local := map[string]string{}
	var decl []string
	for _, id := range ids {
		local[id] = `{"id":"` + id + `","version":"1.0.0"}`
		decl = append(decl, `"./`+id+`":{}`)
	}
	ws, _ := resolveDepsWorkspace(t, "{"+strings.Join(decl, ",")+"}", local)
	out, err := runResolveDeps(t, "--workspace-folder", ws)
	if err != nil {
		t.Fatal(err)
	}
	prev := -1
	for _, id := range ids {
		i := strings.Index(out, "[./"+id+"<")
		if i <= prev {
			t.Fatalf("mermaid nodes not in declaration order %v:\n%s", ids, out)
		}
		prev = i
	}
}
