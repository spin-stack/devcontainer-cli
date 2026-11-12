package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var differentialKnown = map[string]string{
	"dockerfile-with-syntax":           "legacy GitHub Release Feature (accepted limitation)",
	"dockerfile-with-target":           "legacy GitHub Release Feature (accepted limitation)",
	"compose-Dockerfile-with-features": "legacy GitHub Release Feature (accepted limitation)",
	"compose-Dockerfile-with-target":   "legacy GitHub Release Feature (accepted limitation)",
	"compose-image-with-features":      "legacy GitHub Release Feature (accepted limitation)",
}

func TestDifferentialReadConfiguration(t *testing.T) {
	if os.Getenv("PARITY_DIFFERENTIAL") == "" {
		t.Skip("set PARITY_DIFFERENTIAL=1 (task parity:differential): needs the oracle, Docker and the network")
	}
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "reference", "devcontainer.js")
	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Fatalf("TS reference oracle not compiled (task reference): %v", err)
	}
	goCLI := os.Getenv("PARITY_CLI_GO")
	if goCLI == "" {
		goCLI = filepath.Join(repoRoot, "devcontainer")
	}

	fixtures := differentialFixtures(t, filepath.Join(repoRoot, "reference", "src", "test", "configs"))
	for name := range differentialKnown {
		if !slices.Contains(fixtures, name) {
			t.Errorf("differentialKnown lists %q, which is no fixture", name)
		}
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(repoRoot, "reference", "src", "test", "configs", name)
			tsCode, tsOut := runDifferential(t, src, "node", oracle)
			goCode, goOut := runDifferential(t, src, goCLI)
			same := tsCode == goCode && reflect.DeepEqual(tsOut, goOut)
			reason, known := differentialKnown[name]
			switch {
			case same && known:
				t.Errorf("%s no longer differs (%s): remove it from differentialKnown", name, reason)
			case !same && known:
				t.Skipf("known difference: %s", reason)
			case tsCode != goCode:
				t.Errorf("exit code: TS=%d Go=%d\n--- TS\n%s\n--- Go\n%s", tsCode, goCode, pretty(tsOut), pretty(goOut))
			case !same:
				t.Errorf("output differs\n--- TS\n%s\n--- Go\n%s", pretty(tsOut), pretty(goOut))
			}
		})
	}
}

func differentialFixtures(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, cfg := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
			if _, err := os.Stat(filepath.Join(root, e.Name(), cfg)); err == nil {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out
}

func runDifferential(t *testing.T, fixture string, cli ...string) (int, any) {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := copyDir(fixture, ws); err != nil {
		t.Fatal(err)
	}
	args := append(cli[1:], "read-configuration", "--workspace-folder", ws,
		"--include-merged-configuration", "--include-features-configuration")
	cmd := exec.CommandContext(t.Context(), cli[0], args...)
	cmd.Dir = findRepoRoot(t)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", cli, err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var out any
	if last := lines[len(lines)-1]; last != "" {
		normalized := featuresFolderRe.ReplaceAllString(strings.ReplaceAll(last, ws, "<WS>"), "${1}<VERSION>-<MS>")
		if err := json.Unmarshal([]byte(normalized), &out); err != nil {
			out = last
		}
	}
	return code, out
}

var featuresFolderRe = regexp.MustCompile(`(/container-features/)[^/"]+-\d+`)

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
