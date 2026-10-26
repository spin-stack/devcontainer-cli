package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// writeOutdatedWorkspace writes <ws>/.devcontainer/devcontainer.json with the
// given features object, and the lockfile when lock is not empty.
func writeOutdatedWorkspace(t *testing.T, features, lock string) string {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".devcontainer", "devcontainer.json"), `{"image":"alpine","features":`+features+`}`, 0o644)
	if lock != "" {
		writeFile(t, filepath.Join(ws, ".devcontainer", "devcontainer-lock.json"), lock, 0o644)
	}
	return ws
}

func runOutdatedFor(t *testing.T, opts outdatedOpts) (string, error) {
	t.Helper()
	if opts.logLevel == "" {
		opts.logLevel = "info"
	}
	out := &captureOutput{}
	err := runOutdated(t.Context(), out, opts)
	return out.out.String(), err
}

// outdated reports, for each versioned Feature, the newest version its tag
// wants, the newest published one, and the current one (the lockfile's when it
// pins one); Features with no published versions, and local ones, are left out.
func TestOutdatedReportsVersions(t *testing.T) {
	reg := newReadRegistry(t)
	reg.tags["ns/multi"] = []string{"1.0.0", "1.2.0", "2.0.0", "latest"}
	reg.tags["ns/single"] = []string{"3.1.0"}
	reg.tags["ns/blank"] = []string{"1.0.0"}
	reg.tags["ns/none"] = []string{}
	h := reg.host
	features := `{"` + h + `/ns/multi:1":{}, "` + h + `/ns/single:3":{}, "` + h + `/ns/blank:1":{}, "` + h + `/ns/none:1":{}, "./local":{}}`
	lock := `{"features":{"` + h + `/ns/multi:1":{"version":"1.0.0"},"` + h + `/ns/blank:1":{"version":""}}}`
	ws := writeOutdatedWorkspace(t, features, lock)

	got, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, outputFormat: "json"})
	if err != nil {
		t.Fatal(err)
	}
	// stdout is no terminal: TS's JSON.stringify writes it on one line.
	if strings.Count(got, "\n") != 1 || strings.Contains(got, "  ") {
		t.Errorf("json output = %q, want one unindented line", got)
	}
	var doc struct {
		Features map[string]outdatedEntry `json:"features"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got)
	}
	want := map[string]outdatedEntry{
		h + "/ns/multi:1":  {Current: "1.0.0", Wanted: "1.2.0", WantedMajor: "1", Latest: "2.0.0", LatestMajor: "2"},
		h + "/ns/single:3": {Current: "3.1.0", Wanted: "3.1.0", WantedMajor: "3", Latest: "3.1.0", LatestMajor: "3"},
		h + "/ns/blank:1":  {Current: "1.0.0", Wanted: "1.0.0", WantedMajor: "1", Latest: "1.0.0", LatestMajor: "1"},
	}
	if len(doc.Features) != len(want) {
		t.Errorf("features = %v, want %v", doc.Features, want)
	}
	for id, w := range want {
		if doc.Features[id] != w {
			t.Errorf("%s = %+v, want %+v", id, doc.Features[id], w)
		}
	}

	text, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, outputFormat: "text"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "Feature") || !strings.HasPrefix(lines[1], h+"/ns/multi ") {
		t.Errorf("text table:\n%s", text)
	}
}

// With no Features there is nothing to look up: an empty result in JSON, a
// sentence in text.
func TestOutdatedWithoutFeatures(t *testing.T) {
	ws := writeOutdatedWorkspace(t, `{}`, "")
	if got, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, outputFormat: "json"}); err != nil || got != "{\"features\":{}}\n" {
		t.Errorf("json: err = %v, stdout = %q", err, got)
	}
	if got, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, outputFormat: "text"}); err != nil || got != "No features configured.\n" {
		t.Errorf("text: err = %v, stdout = %q", err, got)
	}
}

// --config names the configuration to read, wherever it is; a configuration
// that cannot be loaded, or a log file that cannot be opened, fails the command.
func TestOutdatedConfigAndFailures(t *testing.T) {
	reg := newReadRegistry(t)
	reg.tags["ns/tool"] = []string{"1.0.0"}
	ws := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "custom.json")
	writeFile(t, cfg, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	got, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, configPath: cfg, outputFormat: "json"})
	if err != nil || !strings.Contains(got, reg.host+"/ns/tool:1") {
		t.Errorf("--config: err = %v, stdout = %s", err, got)
	}

	if _, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: t.TempDir(), outputFormat: "json"}); err == nil {
		t.Error("missing configuration: want an error")
	}

	_, err = runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, configPath: cfg, logFile: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "open log file") {
		t.Errorf("log file is a directory: err = %v", err)
	}
}

func TestHighestSatisfyingTagSingleVersion(t *testing.T) {
	v := []*semver.Version{semver.MustParse("1.4.0")}
	if got := highestSatisfyingTag(v, "latest"); got != "1.4.0" {
		t.Errorf("latest of one version = %q, want 1.4.0", got)
	}
}

// Keys of the file that the features map does not hold are not listed.
func TestOrderedFeatureKeysOnlyListsFeatures(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"features":{"b":{},"gone":{},"a":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := orderedFeatureKeys(cfg, map[string]any{"a": nil, "b": nil})
	if want := []string{"b", "a"}; !slices.Equal(got, want) {
		t.Errorf("keys = %q, want %q", got, want)
	}
}

// A lockfile that is not JSON fails outdated as it fails the reference's:
// exit code 1 and nothing on stdout, the SyntaxError's stack logged at debug
// level only (outdated's catch writes it with output.write).
func TestOutdatedFailsOnAMalformedLockfile(t *testing.T) {
	ws := writeOutdatedWorkspace(t, `{"ghcr.io/devcontainers/features/node:1":{}}`, "not json")
	const stack = `SyntaxError: Unexpected token 'o', "not json" is not valid JSON`
	for level, logged := range map[string]bool{"info": false, "debug": true} {
		logFile := filepath.Join(t.TempDir(), "log")
		got, err := runOutdatedFor(t, outdatedOpts{workspaceFolder: ws, outputFormat: "json", logLevel: level, logFile: logFile})
		wantExitCode(t, err, 1)
		if got != "" {
			t.Errorf("%s: stdout = %q, want nothing", level, got)
		}
		data, rerr := os.ReadFile(logFile)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if strings.Contains(string(data), stack) != logged {
			t.Errorf("%s: log =\n%s\nwant the stack logged: %v", level, data, logged)
		}
	}
}
