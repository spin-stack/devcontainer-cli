package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func interopEntries() []imagemeta.Entry {
	yes := true
	return []imagemeta.Entry{
		{
			ID:                "ghcr.io/devcontainers/features/node:1",
			Mounts:            []any{"source=nvm,target=/usr/local/share/nvm,type=volume"},
			ForwardPorts:      []any{float64(3000)},
			ContainerUser:     "node",
			Init:              &yes,
			OnCreateCommand:   "echo feature-oncreate",
			PostCreateCommand: "npm ci",
			Customizations: map[string]any{
				"vscode": map[string]any{
					"extensions": []any{"dbaeumer.vscode-eslint"},
				},
			},
		},
		{
			Mounts:            []any{"source=cfg,target=/cfg,type=volume"},
			ForwardPorts:      []any{float64(8080)},
			RemoteUser:        "node",
			ContainerUser:     "node",
			WaitFor:           "postCreateCommand",
			PostCreateCommand: "echo config-postcreate",
			PostAttachCommand: "echo attached",
			Customizations: map[string]any{
				"vscode": map[string]any{
					"settings": map[string]any{"editor.formatOnSave": true},
				},
			},
		},
	}
}

func jsonNormEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v (%s)", err, b)
	}
	return reflect.DeepEqual(av, bv)
}

func TestMetadataInteropWhitespaceInvariant(t *testing.T) {
	entries := interopEntries()

	goLabel := imagemeta.GenerateMetadataLabel(entries)
	if goLabel == "" || goLabel[0] != '[' {
		t.Fatalf("Go must emit a JSON array label even for multiple entries, got: %s", goLabel)
	}

	var parsed any
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

	goMerged := mergeFromLabel(t, goLabel)
	tsMerged := mergeFromLabel(t, string(tsStyle))
	if !jsonNormEqual(t, goMerged, tsMerged) {
		t.Fatalf("merged config differs across label whitespace:\n--- go\n%s\n--- ts\n%s", goMerged, tsMerged)
	}

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
	if len(mc.PostCreateCommands) != 2 {
		t.Errorf("expected 2 postCreate commands, got %d", len(mc.PostCreateCommands))
	}
	if len(mc.OnCreateCommands) != 1 {
		t.Errorf("expected 1 onCreate command, got %d", len(mc.OnCreateCommands))
	}
	if got := len(mc.Customizations["vscode"]); got != 2 {
		t.Errorf("expected 2 vscode customization contributions, got %d", got)
	}
}

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

func TestMetadataInteropGoRoundTrip(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("docker required")
	}
	ctx := t.Context()
	entries := interopEntries()

	expected := mergeFromLabel(t, imagemeta.GenerateMetadataLabel(entries))

	info := imagemeta.GenerateComposeLabelBuild("scratch", entries, "")
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

func TestMetadataInteropTSToGo(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "reference", "devcontainer.js")
	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference oracle not compiled (task reference)")
	}
	if !isDockerAvailable() {
		t.Skip("docker required")
	}
	ctx := t.Context()

	ws := t.TempDir()
	for path, content := range map[string]string{
		".devcontainer/devcontainer.json": `{
	"build": {"dockerfile": "Dockerfile"},
	"features": {"./feat": {}},
	"remoteUser": "root",
	"containerEnv": {"FROM_CONFIG": "1"},
	"capAdd": ["SYS_PTRACE"],
	"mounts": ["type=volume,source=interop-vol,target=/interop"],
	"postCreateCommand": "echo config",
	"customizations": {"vscode": {"extensions": ["a.b"]}}
}`,
		".devcontainer/Dockerfile":                     "FROM alpine:3\n",
		".devcontainer/feat/devcontainer-feature.json": `{"id": "feat", "version": "1.0.0", "containerEnv": {"FROM_FEATURE": "2"}, "init": true, "postStartCommand": "echo feature"}`,
		".devcontainer/feat/install.sh":                "#!/bin/sh\necho installed\n",
	} {
		full := filepath.Join(ws, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	label := func(tag string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "inspect", "--format",
			`{{ index .Config.Labels "`+imagemeta.MetadataLabel+`" }}`, tag).Output()
		if err != nil {
			t.Fatalf("docker inspect %s: %v", tag, err)
		}
		return string(bytes.TrimRight(out, "\n"))
	}

	const tsTag, goTag = "dcc-interop-ts:test", "dcc-interop-go:test"
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tsTag, goTag).Run() })
	if out, err := exec.CommandContext(ctx, "node", oracle, "build", "--workspace-folder", ws, "--image-name", tsTag).CombinedOutput(); err != nil {
		t.Fatalf("TS build: %v\n%s", err, out)
	}
	assertOutcome(t, runCLI(t, "build", "--workspace-folder", ws, "--image-name", goTag), "success")

	tsMerged, goMerged := mergeFromLabel(t, label(tsTag)), mergeFromLabel(t, label(goTag))
	if !jsonNormEqual(t, tsMerged, goMerged) {
		t.Fatalf("the label TS wrote merges differently from Go's:\n--- TS\n%s\n--- Go\n%s", tsMerged, goMerged)
	}
}
