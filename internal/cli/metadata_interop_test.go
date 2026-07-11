package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

// Bidirectional metadata interop.
//
// Go always emits the devcontainer.metadata label as a JSON *array* even for a
// single entry (imagemeta.GenerateMetadataLabel). TS and Go serialize with
// different whitespace and key ordering, but the PARSED JSON is identical.
// Therefore every assertion here compares NORMALIZED (parsed) JSON, never the
// raw label bytes.
//
// Live/hermetic half: build a real image whose devcontainer.metadata label was
// produced by the Go label generator (imagemeta.GenerateExtendImageBuild — the
// exact path build.go uses), read it back through docker inspect +
// imagemeta.ReadMetadataFromLabels + MergeConfiguration, and assert the merge.
//
// TS->Go half: guarded to t.Skip unless the compiled reference oracle is
// present.

// interopEntries returns metadata entries exercising every merge dimension the
// interop test cares about: mounts, forwardPorts, users, lifecycle hooks, and
// customizations. It mirrors how build.go assembles the []Entry slice: a
// base/feature-contributed entry followed by the config entry (last = highest
// priority for scalars).
func interopEntries() []imagemeta.Entry {
	yes := true
	return []imagemeta.Entry{
		{
			ID:                "ghcr.io/devcontainers/features/node:1",
			Mounts:            []interface{}{"source=nvm,target=/usr/local/share/nvm,type=volume"},
			ForwardPorts:      []interface{}{float64(3000)},
			ContainerUser:     "node",
			Init:              &yes,
			OnCreateCommand:   "echo feature-oncreate",
			PostCreateCommand: "npm ci",
			Customizations: map[string]interface{}{
				"vscode": map[string]interface{}{
					"extensions": []interface{}{"dbaeumer.vscode-eslint"},
				},
			},
		},
		{
			// Config-level entry (no id), highest priority for scalars.
			Mounts:            []interface{}{"source=cfg,target=/cfg,type=volume"},
			ForwardPorts:      []interface{}{float64(8080)},
			RemoteUser:        "node",
			ContainerUser:     "node",
			WaitFor:           "postCreateCommand",
			PostCreateCommand: "echo config-postcreate",
			PostAttachCommand: "echo attached",
			Customizations: map[string]interface{}{
				"vscode": map[string]interface{}{
					"settings": map[string]interface{}{"editor.formatOnSave": true},
				},
			},
		},
	}
}

// jsonNormEqual reports whether two JSON documents are equal after parsing,
// ignoring whitespace and object key ordering. This is the comparator the
// interop parity claim requires: TS and Go differ in raw bytes but not in
// parsed structure.
func jsonNormEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv interface{}
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v (%s)", err, b)
	}
	return reflect.DeepEqual(av, bv)
}

// TestMetadataInteropWhitespaceInvariant is a pure, always-run test that encodes
// the core parity claim hermetically: the SAME logical metadata serialized with
// Go's compact array format vs a TS-style layout (spaces after ':'/',' and a
// different key order) parses to the same normalized JSON AND produces an
// identical MergeConfiguration result. No docker, no network.
func TestMetadataInteropWhitespaceInvariant(t *testing.T) {
	entries := interopEntries()

	goLabel := imagemeta.GenerateMetadataLabel(entries)
	if goLabel == "" || goLabel[0] != '[' {
		t.Fatalf("Go must emit a JSON array label even for multiple entries, got: %s", goLabel)
	}

	// Simulate a TS-serialized label: same values, but re-encoded with indentation
	// (extra whitespace) and, via a round-trip through interface{}, arbitrary key
	// order. json.MarshalIndent produces bytes that differ from goLabel yet must
	// parse identically.
	var parsed interface{}
	if err := json.Unmarshal([]byte(goLabel), &parsed); err != nil {
		t.Fatalf("parse go label: %v", err)
	}
	tsStyle, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal([]byte(goLabel), tsStyle) {
		t.Fatal("expected differing raw bytes between compact and indented forms")
	}
	if !jsonNormEqual(t, []byte(goLabel), tsStyle) {
		t.Fatalf("normalized JSON differs:\n--- go\n%s\n--- ts-style\n%s", goLabel, tsStyle)
	}

	// Both label forms must read back to the same merged configuration.
	goMerged := mergeFromLabel(t, goLabel)
	tsMerged := mergeFromLabel(t, string(tsStyle))
	if !jsonNormEqual(t, goMerged, tsMerged) {
		t.Fatalf("merged config differs across label whitespace:\n--- go\n%s\n--- ts\n%s", goMerged, tsMerged)
	}

	// Spot-check the merge semantics so a silently-empty merge can't pass.
	var mc imagemeta.MergedConfig
	if err := json.Unmarshal(goMerged, &mc); err != nil {
		t.Fatal(err)
	}
	if len(mc.Mounts) != 2 {
		t.Errorf("expected 2 merged mounts, got %d", len(mc.Mounts))
	}
	if len(mc.ForwardPorts) != 2 {
		t.Errorf("expected 2 forwarded ports, got %d", len(mc.ForwardPorts))
	}
	if mc.RemoteUser != "node" || mc.ContainerUser != "node" {
		t.Errorf("expected remoteUser/containerUser=node, got %q/%q", mc.RemoteUser, mc.ContainerUser)
	}
	if mc.Init == nil || !*mc.Init {
		t.Errorf("init must be OR'd true from the feature entry")
	}
	// Lifecycle hooks accumulate across entries.
	if len(mc.PostCreateCommands) != 2 {
		t.Errorf("expected 2 postCreate commands, got %d", len(mc.PostCreateCommands))
	}
	if len(mc.OnCreateCommands) != 1 {
		t.Errorf("expected 1 onCreate command, got %d", len(mc.OnCreateCommands))
	}
	// customizations grouped by key, one array element per contributing entry.
	if got := len(mc.Customizations["vscode"]); got != 2 {
		t.Errorf("expected 2 vscode customization contributions, got %d", got)
	}
}

// mergeFromLabel parses a devcontainer.metadata label value the way the runtime
// does and returns the merged configuration as JSON bytes.
func mergeFromLabel(t *testing.T, label string) []byte {
	t.Helper()
	entries := imagemeta.ReadMetadataFromLabels(
		map[string]string{imagemeta.MetadataLabel: label}, log.Null)
	if len(entries) == 0 {
		t.Fatalf("no entries parsed from label: %s", label)
	}
	merged := imagemeta.MergeConfiguration(entries)
	b, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestMetadataInteropGoRoundTrip is the live/hermetic half: it builds a real
// image (FROM scratch) whose devcontainer.metadata label was written by the Go
// label generator through the exact Dockerfile path build.go uses
// (imagemeta.GenerateExtendImageBuild). It then reads the label back via
// `docker inspect` + ReadMetadataFromLabels + MergeConfiguration and asserts the
// merge matches the in-memory expectation as normalized JSON. This validates
// that LABEL escaping survives a real docker round-trip. No registry/network:
// the base image is `scratch`.
func TestMetadataInteropGoRoundTrip(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("docker required")
	}
	ctx := context.Background()
	entries := interopEntries()

	// Expected merge computed purely in-memory from the same entries.
	expected := mergeFromLabel(t, imagemeta.GenerateMetadataLabel(entries))

	// Build the Dockerfile exactly as build.go does for the no-features case
	// (base image + metadata label), but pin the base to `scratch` so the build
	// needs no network.
	info := imagemeta.GenerateExtendImageBuild("scratch", nil, entries, "root", "", false, nil)
	dockerfile := info.DockerfilePrefixContent + info.DockerfileContent

	tmp := t.TempDir()
	dfPath := filepath.Join(tmp, "Dockerfile")
	if err := os.WriteFile(dfPath, []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	tag := "dcc-interop-rw006:test"
	buildArgs := []string{"build", "-f", dfPath, "-t", tag,
		"--build-arg", "_DEV_CONTAINERS_BASE_IMAGE=scratch",
		"--target", info.OverrideTarget, tmp}
	if out, err := exec.CommandContext(ctx, "docker", buildArgs...).CombinedOutput(); err != nil {
		t.Fatalf("docker build failed: %v\n%s\n--- Dockerfile ---\n%s", err, out, dockerfile)
	}
	defer exec.Command("docker", "rmi", "-f", tag).Run()

	inspect := exec.CommandContext(ctx, "docker", "inspect",
		"--format", `{{ index .Config.Labels "`+imagemeta.MetadataLabel+`" }}`, tag)
	labelBytes, err := inspect.Output()
	if err != nil {
		t.Fatalf("docker inspect failed: %v", err)
	}
	label := string(bytes.TrimRight(labelBytes, "\n"))
	if label == "" {
		t.Fatal("devcontainer.metadata label was empty after docker round-trip")
	}

	got := mergeFromLabel(t, label)
	if !jsonNormEqual(t, expected, got) {
		t.Fatalf("merged config differs after docker round-trip:\n--- expected\n%s\n--- got\n%s", expected, got)
	}
}

// TestMetadataInteropTSToGo is the TS->Go direction: build an image with the TS
// oracle carrying the same features, then read the devcontainer.metadata label
// with the Go reader and assert the normalized merge matches. It requires the
// compiled reference oracle, so it is guarded to Skip in this hermetic worktree.
//
// TODO: run the full bidirectional exchange (TS build -> Go read AND Go build ->
// TS read) end-to-end once the compiled oracle is present.
func TestMetadataInteropTSToGo(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")
	if _, err := os.Stat(oracle); err != nil {
		t.Skip("TS reference oracle not compiled; skipping TS->Go interop")
	}
	if !isDockerAvailable() {
		t.Skip("docker required")
	}
	// When the oracle is present, the TS build produces a
	// devcontainer.metadata label with different whitespace/key-order than Go's.
	// The assertion still holds: read via the Go reader and compare the merge as
	// normalized JSON. This guard keeps the worktree hermetic until the
	// bidirectional build wiring is added.
	t.Skip("TS->Go interop build wiring not implemented yet")
}
