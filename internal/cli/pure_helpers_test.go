package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/doctor"
	"github.com/devcontainers/cli/internal/oci"
)

func TestGetFeatureIdWithoutVersion(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/devcontainers/features/go:1.2.3": "ghcr.io/devcontainers/features/go",
		"ghcr.io/devcontainers/features/go:1":     "ghcr.io/devcontainers/features/go",
		"ghcr.io/devcontainers/features/go":       "ghcr.io/devcontainers/features/go", // no version
		// A digest pin: the leftmost delimiter is '@', so the whole @sha256:... goes.
		"ghcr.io/o/r@sha256:abcdef": "ghcr.io/o/r",
		// A registry port colon is NOT the version (it is followed by a '/').
		"localhost:5000/o/r:2": "localhost:5000/o/r",
		"localhost:5000/o/r":   "localhost:5000/o/r",
		"":                     "",
	}
	for in, want := range cases {
		if got := getFeatureIdWithoutVersion(in); got != want {
			t.Errorf("getFeatureIdWithoutVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustVersions(t *testing.T, tags ...string) []*semver.Version {
	t.Helper()
	vs := make([]*semver.Version, 0, len(tags))
	for _, tg := range tags {
		v, err := semver.NewVersion(tg)
		if err != nil {
			t.Fatalf("bad version %q: %v", tg, err)
		}
		vs = append(vs, v)
	}
	return vs // caller supplies them already ascending
}

func TestHighestSatisfyingTag(t *testing.T) {
	vs := mustVersions(t, "1.0.0", "1.2.0", "1.4.1", "2.0.0", "2.3.0")
	cases := []struct {
		name, tag, want string
		versions        []*semver.Version
	}{
		{"empty set", "1", "", nil},
		{"latest picks the highest", "latest", "2.3.0", vs},
		{"empty tag picks the highest", "", "2.3.0", vs},
		{"major 1 picks highest in that major", "1", "1.4.1", vs},
		{"major 2 picks highest in that major", "2", "2.3.0", vs},
		{"specific minor still resolves within its major", "1.2.0", "1.4.1", vs},
		{"unsatisfiable major", "3", "", vs},
		{"invalid tag", "not-a-version", "", vs},
	}
	for _, c := range cases {
		if got := highestSatisfyingTag(c.versions, c.tag); got != c.want {
			t.Errorf("%s: highestSatisfyingTag(_, %q) = %q, want %q", c.name, c.tag, got, c.want)
		}
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"1.2.3":      "1",
		"2":          "2",
		"0.5.0":      "0",
		"not-semver": "",
		"10.20.30":   "10",
		"v3.1.0":     "3", // semver tolerates a leading v
	}
	for in, want := range cases {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAppPortPublishArgs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", ``, nil},
		{"null", `null`, nil},
		{"invalid json", `{`, nil},
		{"single number binds to localhost", `3000`, []string{"-p", "127.0.0.1:3000:3000"}},
		{"single string passes through", `"8080:80"`, []string{"-p", "8080:80"}},
		{"empty string is skipped", `""`, nil},
		{"array of mixed", `[3000, "9000:9000"]`, []string{"-p", "127.0.0.1:3000:3000", "-p", "9000:9000"}},
		{"object is ignored (no number/string members)", `{"a":1}`, nil},
	}
	for _, c := range cases {
		var raw json.RawMessage
		if c.raw != "" {
			raw = json.RawMessage(c.raw)
		}
		got := appPortPublishArgs(raw)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: appPortPublishArgs(%s) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}

func TestFolderImageName(t *testing.T) {
	// Deterministic: same path -> same name; prefixed with vsc-<basename>-.
	a := folderImageName("/home/user/My Project")
	b := folderImageName("/home/user/My Project")
	if a != b {
		t.Errorf("folderImageName not deterministic: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, "vsc-") {
		t.Errorf("missing vsc- prefix: %q", a)
	}
	// Different paths -> different names.
	if folderImageName("/a") == folderImageName("/b") {
		t.Error("distinct paths produced the same image name")
	}
	// Docker image names are lowercase; the sanitizer must not leave spaces/upper.
	if a != strings.ToLower(a) || strings.ContainsAny(a, " ") {
		t.Errorf("image name not sanitized: %q", a)
	}
}

func TestEnvSliceToMap(t *testing.T) {
	got := envSliceToMap([]string{
		"FOO=bar",
		"EMPTY=",
		"HAS=eq=in=value",
		"no_equals_is_dropped",
		"DUP=first",
		"DUP=second",
	})
	want := map[string]string{
		"FOO":   "bar",
		"EMPTY": "",
		"HAS":   "eq=in=value",
		"DUP":   "second", // last wins
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["no_equals_is_dropped"]; ok {
		t.Error("an entry without '=' must be dropped")
	}
}

func TestSplitExecArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantFlags   []string
		wantCommand []string
	}{
		{
			name:        "double dash separates flags from command",
			args:        []string{"--workspace-folder", "/w", "--", "echo", "hi"},
			wantFlags:   []string{"--workspace-folder", "/w"},
			wantCommand: []string{"echo", "hi"},
		},
		{
			name:        "first bare word starts the command",
			args:        []string{"--config", "dc.json", "ls", "-la"},
			wantFlags:   []string{"--config", "dc.json"},
			wantCommand: []string{"ls", "-la"},
		},
		{
			name:        "value flag consumes its argument",
			args:        []string{"--id-label", "a=b", "--", "sh"},
			wantFlags:   []string{"--id-label", "a=b"},
			wantCommand: []string{"sh"},
		},
		{
			name:        "trailing value flag without a value does not panic",
			args:        []string{"--log-level"},
			wantFlags:   []string{"--log-level"},
			wantCommand: nil,
		},
		{
			name:        "only a command",
			args:        []string{"printenv"},
			wantFlags:   nil,
			wantCommand: []string{"printenv"},
		},
	}
	for _, c := range cases {
		flags, cmd := splitExecArgs(c.args)
		if strings.Join(flags, "\x00") != strings.Join(c.wantFlags, "\x00") {
			t.Errorf("%s: flags = %v, want %v", c.name, flags, c.wantFlags)
		}
		if strings.Join(cmd, "\x00") != strings.Join(c.wantCommand, "\x00") {
			t.Errorf("%s: command = %v, want %v", c.name, cmd, c.wantCommand)
		}
	}
}

func TestDashIfEmpty(t *testing.T) {
	if dashIfEmpty("") != "-" {
		t.Error(`dashIfEmpty("") must be "-"`)
	}
	if dashIfEmpty("x") != "x" {
		t.Error(`dashIfEmpty("x") must be "x"`)
	}
}

func TestTextTable(t *testing.T) {
	got := textTable([][]string{
		{"ID", "CURRENT", "WANTED"},
		{"go", "1.2.3", "1.4.0"},
		{"node", "18", "20"},
	})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), got)
	}
	// Columns are padded to the widest cell and separated by two spaces.
	if lines[0] != "ID    CURRENT  WANTED" {
		t.Errorf("header padding wrong: %q", lines[0])
	}
	// Trailing whitespace on the last column is trimmed.
	for _, l := range lines {
		if l != strings.TrimRight(l, " ") {
			t.Errorf("line has trailing spaces: %q", l)
		}
	}
	// A ragged row (fewer columns) must not panic.
	_ = textTable([][]string{{"a", "b"}, {"c"}})
	if textTable(nil) != "" {
		t.Error("empty table should be empty string")
	}
}

func TestBuildArgsAndOptionsFromConfig(t *testing.T) {
	// nil Build -> empty/nil.
	if m := buildArgsFromConfig(&config.DevContainer{}); len(m) != 0 {
		t.Errorf("buildArgsFromConfig(nil Build) = %v, want empty", m)
	}
	if o := buildOptionsFromConfig(&config.DevContainer{}); o != nil {
		t.Errorf("buildOptionsFromConfig(nil Build) = %v, want nil", o)
	}
	cfg := &config.DevContainer{Build: &config.Build{
		Args:    map[string]string{"K": "V"},
		Options: []string{"--pull"},
	}}
	if m := buildArgsFromConfig(cfg); m["K"] != "V" {
		t.Errorf("buildArgsFromConfig = %v", m)
	}
	if o := buildOptionsFromConfig(cfg); len(o) != 1 || o[0] != "--pull" {
		t.Errorf("buildOptionsFromConfig = %v", o)
	}
}

func TestIsUserFacingBuildError(t *testing.T) {
	if !isUserFacingBuildError(errorString(legacyFeaturePrefix + "foo' ...")) {
		t.Error("a legacy-feature error must be user-facing")
	}
	if isUserFacingBuildError(errorString("some internal failure")) {
		t.Error("an arbitrary error must not be user-facing")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestEncloseStringInBox(t *testing.T) {
	// Box width must match the rune count, not the byte count (unicode-safe).
	out := encloseStringInBox("héllo")
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	top, bottom := []rune(lines[0]), []rune(lines[2])
	// "┌" + 5 bars + "┐"
	if len(top) != 7 || len(bottom) != 7 {
		t.Errorf("box borders not sized to rune width: top=%d bottom=%d", len(top), len(bottom))
	}
	if !strings.HasPrefix(lines[0], "┌") || !strings.HasSuffix(lines[0], "┐") {
		t.Errorf("top border malformed: %q", lines[0])
	}
}

func TestWriteSuccessJSON(t *testing.T) {
	out := &captureOutput{}
	if err := writeSuccessJSON(out, map[string]interface{}{"outcome": "success", "n": 2}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, out.out.String())
	}
	if got["outcome"] != "success" {
		t.Errorf("got %v", got)
	}
}

func TestAddFeatureOption(t *testing.T) {
	// Existing options are preserved and the key is set to true.
	got := addFeatureOption(map[string]interface{}{"version": "1"}, "gradle")
	if got["version"] != "1" || got["gradle"] != true {
		t.Errorf("addFeatureOption merge wrong: %v", got)
	}
	// A non-map option value (e.g. bool/string) is replaced by a fresh map.
	got = addFeatureOption(true, "maven")
	if got["maven"] != true || len(got) != 1 {
		t.Errorf("addFeatureOption(non-map) = %v", got)
	}
}

func TestOCIAnnotationID(t *testing.T) {
	mk := func(ann map[string]string) *oci.ManifestContainer {
		return &oci.ManifestContainer{Manifest: &oci.Manifest{Annotations: ann}}
	}
	if id := ociAnnotationID(mk(map[string]string{"dev.containers.metadata": `{"id":"go"}`})); id != "go" {
		t.Errorf("id = %q, want go", id)
	}
	if id := ociAnnotationID(mk(map[string]string{"dev.containers.metadata": ``})); id != "" {
		t.Errorf("empty metadata must yield empty id, got %q", id)
	}
	if id := ociAnnotationID(mk(map[string]string{"dev.containers.metadata": `not-json`})); id != "" {
		t.Errorf("bad metadata JSON must yield empty id, got %q", id)
	}
	if id := ociAnnotationID(mk(nil)); id != "" {
		t.Errorf("no annotations must yield empty id, got %q", id)
	}
}

func TestAppliedAny(t *testing.T) {
	if appliedAny(nil) {
		t.Error("nil actions -> false")
	}
	if appliedAny([]doctor.Action{{Applied: false}, {Applied: false}}) {
		t.Error("no applied action -> false")
	}
	if !appliedAny([]doctor.Action{{Applied: false}, {Applied: true}}) {
		t.Error("one applied action -> true")
	}
}

func TestFeatureKeyOrderFromFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "devcontainer.json")
	// JSONC with comments + deliberately non-alphabetical feature order.
	os.WriteFile(cfg, []byte(`{
		// a comment
		"image": "x",
		"features": {
			"ghcr.io/z/z:1": {},
			"ghcr.io/a/a:1": {}
		}
	}`), 0o644)

	keys, err := featureKeyOrderFromFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "ghcr.io/z/z:1" || keys[1] != "ghcr.io/a/a:1" {
		t.Errorf("insertion order not preserved: %v", keys)
	}

	// Empty path -> nil, no error.
	if ks, err := featureKeyOrderFromFile(""); err != nil || ks != nil {
		t.Errorf("empty path: got %v, %v", ks, err)
	}
}

func TestOrderedFeatureKeys_AppendsMissingSorted(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "devcontainer.json")
	os.WriteFile(cfg, []byte(`{"features":{"b":{},"a":{}}}`), 0o644)

	// The map has one key NOT present in the file ("z"); it must be appended,
	// and the never-dropped invariant must hold.
	feats := map[string]interface{}{"a": struct{}{}, "b": struct{}{}, "z": struct{}{}}
	got := orderedFeatureKeys(cfg, feats)
	if len(got) != 3 {
		t.Fatalf("want 3 keys, got %v", got)
	}
	// File order first (b, a), then the missing one appended.
	if got[0] != "b" || got[1] != "a" || got[2] != "z" {
		t.Errorf("order = %v, want [b a z]", got)
	}
}

func TestProxyEnvFromEnviron(t *testing.T) {
	// Only the known proxy keys are captured; others are ignored.
	t.Setenv("HTTPS_PROXY", "http://proxy:8080")
	t.Setenv("NO_PROXY", "localhost")
	t.Setenv("UNRELATED_VAR", "x")
	m := proxyEnvFromEnviron()
	if m["HTTPS_PROXY"] != "http://proxy:8080" || m["NO_PROXY"] != "localhost" {
		t.Errorf("proxy env not captured: %v", m)
	}
	if _, ok := m["UNRELATED_VAR"]; ok {
		t.Error("non-proxy var captured")
	}
}

func TestIsBareVersion(t *testing.T) {
	cases := map[string]bool{
		"1": true, "1.2": true, "1.2.3": true,
		"": false, "1.2.3.4": false, "v1": false,
		"1.": false, ".1": false, "1.x": false, "latest": false, "1.2.beta": false,
	}
	for in, want := range cases {
		if got := isBareVersion(in); got != want {
			t.Errorf("isBareVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestFindPlatformArg covers #1241: extracting --platform from runArgs in both
// "--platform=X" and "--platform X" forms.
func TestFindPlatformArg(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--platform=linux/amd64"}, "linux/amd64"},
		{[]string{"--platform", "linux/arm64"}, "linux/arm64"},
		{[]string{"--cap-add=SYS_PTRACE", "--platform=linux/amd64", "--rm"}, "linux/amd64"},
		{[]string{"--rm"}, ""},
		{nil, ""},
		{[]string{"--platform"}, ""}, // dangling flag, no value
	}
	for _, tc := range cases {
		if got := findPlatformArg(tc.args); got != tc.want {
			t.Errorf("findPlatformArg(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// TestBuildSecretIDs covers #1078: extracting the KEY from each "KEY=VALUE"
// build secret for mounting as --mount=type=secret,id=KEY.
func TestBuildSecretIDs(t *testing.T) {
	got := buildSecretIDs([]string{"NPM_TOKEN=abc", "GH_TOKEN=xyz=with=eq", "=novalue", "noeq"})
	want := []string{"NPM_TOKEN", "GH_TOKEN"}
	if len(got) != len(want) {
		t.Fatalf("buildSecretIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(buildSecretIDs(nil)) != 0 {
		t.Error("buildSecretIDs(nil) should be empty")
	}
}
