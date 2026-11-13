package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
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

func TestPlainHTTPFeatureIDIsNotATarball(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(featureTgzBytes(t, map[string]string{"devcontainer-feature.json": `{"id":"x"}`}))
	}))
	t.Cleanup(srv.Close)
	id := srv.URL + "/devcontainer-feature-x.tgz"
	wantLogs := []string{
		"Could not resolve Feature manifest for '" + id + "'.  If necessary, provide registry credentials with 'docker login <registry>'.\n",
		"Could not resolve Feature '" + id + "'.  Ensure the Feature is published and accessible from your current environment.\n",
	}

	for name, process := range map[string]func(log.Logger) (*features.Set, error){
		"install": func(l log.Logger) (*features.Set, error) {
			return processInstallFeature(l, nil, &features.FNode{UserFeatureID: id}, localFeatureRoots{}, nil, stageDirs(t))
		},
		"metadata": func(l log.Logger) (*features.Set, error) {
			return newMetadataProcessFeature(nil, l, localFeatureRoots{}, nil)(&features.FNode{UserFeatureID: id})
		},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			set, err := process(log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: &logs}))
			if set != nil || err != nil {
				t.Fatalf("set, err = %+v, %v; want neither", set, err)
			}
			for _, line := range wantLogs {
				if !strings.Contains(logs.String(), line) {
					t.Errorf("logs = %q, want %q", logs.String(), line)
				}
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests to the http:// URL, want none", n)
	}
}

func TestUnparseableFeatureReferences(t *testing.T) {
	for id, want := range map[string]string{
		"owner/repo/feat@1@2":    "Parse error. Use the '@' symbol only to designate a version tag.",
		"owner/repo/fe.at@bad":   "Could not resolve Feature 'owner/repo/fe.at@bad'.  Ensure the Feature is published and accessible from your current environment.",
		"owner/repo/sub/feat@v1": "Could not resolve Feature 'owner/repo/sub/feat@v1'.  Ensure the Feature is published and accessible from your current environment.",
		"owner//feat@v1":         "Could not resolve Feature 'owner//feat@v1'.  Ensure the Feature is published and accessible from your current environment.",
	} {
		var logs bytes.Buffer
		set, err := newMetadataProcessFeature(nil, log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: &logs}), localFeatureRoots{}, nil)(&features.FNode{UserFeatureID: id})
		if set != nil || err != nil {
			t.Errorf("%s: set, err = %+v, %v; want neither", id, set, err)
		}
		if !strings.HasSuffix(logs.String(), want+"\n") {
			t.Errorf("%s: logs = %q, want them to end with %q", id, logs.String(), want)
		}
	}
	if _, err := newMetadataProcessFeature(nil, log.Null, localFeatureRoots{}, nil)(&features.FNode{UserFeatureID: "owner/repo/feat@v1"}); err == nil || !strings.HasPrefix(err.Error(), "ERR: Feature 'owner/repo/feat@v1' could not be processed.  ") {
		t.Errorf("err = %v; want the reference's parse failure", err)
	}
}
