package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
)

// resolveDepsWorkspace writes a workspace whose devcontainer.json lists the
// given features, with each local Feature (name -> devcontainer-feature.json)
// in the .devcontainer folder, and returns the workspace and config paths.
func resolveDepsWorkspace(t *testing.T, featuresJSON string, local map[string]string) (string, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(configPath, []byte(`{"image":"img","features":`+featuresJSON+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, meta := range local {
		writeFeature(t, dir, name, meta)
	}
	return ws, configPath
}

func runResolveDeps(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := realFeaturesResolveDepsCmd()
	// The root command silences usage and errors; the bare subcommand does not.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

// resolve-dependencies prints the mermaid graph, then the install order with
// each Feature's options, dependencies first.
func TestFeaturesResolveDependencies(t *testing.T) {
	ws, _ := resolveDepsWorkspace(t, `{"./a":{"x":"1"}}`, map[string]string{
		"a": `{"id":"a","dependsOn":{"./b":{"y":"2"}}}`,
		"b": `{"id":"b"}`,
	})
	want := []map[string]any{
		{"id": "./b", "options": map[string]any{"y": "2"}},
		{"id": "./a", "options": map[string]any{"x": "1"}},
	}
	check := func(t *testing.T, out string) {
		t.Helper()
		mermaid, jsonPart, ok := strings.Cut(out, "\n\n")
		if !ok || !strings.HasPrefix(mermaid, "flowchart\n") || !strings.Contains(mermaid, "-->") {
			t.Fatalf("output = %q, want the mermaid graph first", out)
		}
		var got struct {
			InstallOrder []map[string]any `json:"installOrder"`
		}
		if err := json.Unmarshal([]byte(jsonPart), &got); err != nil {
			t.Fatalf("install order %q: %v", jsonPart, err)
		}
		if !reflect.DeepEqual(got.InstallOrder, want) {
			t.Errorf("installOrder = %v, want %v", got.InstallOrder, want)
		}
	}

	t.Run("workspace folder flag", func(t *testing.T) {
		t.Chdir(t.TempDir())
		out, err := runResolveDeps(t, "--workspace-folder", ws)
		if err != nil {
			t.Fatal(err)
		}
		check(t, out)
	})
	t.Run("current directory", func(t *testing.T) {
		t.Chdir(ws)
		out, err := runResolveDeps(t)
		if err != nil {
			t.Fatal(err)
		}
		check(t, out)
	})
}

func TestFeaturesResolveDependenciesFailures(t *testing.T) {
	t.Run("no features exits 1 with nothing on stdout", func(t *testing.T) {
		ws, _ := resolveDepsWorkspace(t, `{}`, nil)
		out, err := runResolveDeps(t, "--workspace-folder", ws)
		if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 || out != "" {
			t.Fatalf("err = %v, stdout = %q; want exit 1 and no output", err, out)
		}
	})
	t.Run("no configuration", func(t *testing.T) {
		if out, err := runResolveDeps(t, "--workspace-folder", t.TempDir()); err == nil || out != "" {
			t.Fatalf("err = %v, stdout = %q; want an error", err, out)
		}
	})
	t.Run("missing Feature", func(t *testing.T) {
		ws, _ := resolveDepsWorkspace(t, `{"./gone":{}}`, nil)
		if out, err := runResolveDeps(t, "--workspace-folder", ws); err == nil || !strings.Contains(err.Error(), "'./gone' could not be processed") || out != "" {
			t.Fatalf("err = %v, stdout = %q", err, out)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		ws, _ := resolveDepsWorkspace(t, `{"./a":{}}`, map[string]string{
			"a": `{"id":"a","dependsOn":{"./b":{}}}`,
			"b": `{"id":"b","dependsOn":{"./a":{}}}`,
		})
		if out, err := runResolveDeps(t, "--workspace-folder", ws); err == nil || !strings.Contains(err.Error(), "circular dependency") || out != "" {
			t.Fatalf("err = %v, stdout = %q", err, out)
		}
	})
	t.Run("tarball differing from the lockfile", func(t *testing.T) {
		uri := serveBytes(t, featureTgzBytes(t, map[string]string{"devcontainer-feature.json": `{"id":"tb"}`}), http.StatusOK) + "/f.tgz"
		ws, configPath := resolveDepsWorkspace(t, fmt.Sprintf(`{%q:{}}`, uri), nil)
		lock := fmt.Sprintf(`{"features":{%q:{"version":"1","resolved":%q,"integrity":"sha256:%s"}}}`, uri, uri, strings.Repeat("0", 64))
		if err := os.WriteFile(features.LockfilePath(configPath), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runResolveDeps(t, "--workspace-folder", ws); err == nil || err.Error() != "Digest did not match for "+uri+"." {
			t.Fatalf("err = %v, want the lockfile's digest mismatch", err)
		}
	})
}
