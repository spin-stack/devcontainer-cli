package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func TestRootCommand_Version(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--version"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.Execute()
	// Bare version output (like the TS CLI), no "<name> version" prefix.
	got := strings.TrimSpace(buf.String())
	if got == "" || strings.Contains(got, " ") {
		t.Errorf("expected bare version output, got %q", buf.String())
	}
}

func TestRootCommand_Help(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--help"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.Execute()
	out := buf.String()

	// All commands should be listed
	commands := []string{"build", "up", "exec", "read-configuration", "set-up",
		"run-user-commands", "outdated", "upgrade", "features", "templates"}
	for _, cmd := range commands {
		if !strings.Contains(out, cmd) {
			t.Errorf("help output missing command %q", cmd)
		}
	}
}

func TestRootCommand_FeaturesHelp(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"features", "--help"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.Execute()
	out := buf.String()

	subcommands := []string{"test", "package", "publish", "info", "resolve-dependencies", "generate-docs"}
	for _, sub := range subcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("features help missing subcommand %q", sub)
		}
	}
}

func TestRootCommand_TemplatesHelp(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"templates", "--help"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.Execute()
	out := buf.String()

	subcommands := []string{"apply", "publish", "metadata", "generate-docs"}
	for _, sub := range subcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("templates help missing subcommand %q", sub)
		}
	}
}

func TestReadConfiguration_ImageFixture(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", "image")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"read-configuration", "--workspace-folder", fixtureDir})
	err := root.Execute()

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should be valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\nOutput: %s", err, output)
	}

	// Should have configuration
	cfg, ok := result["configuration"]
	if !ok {
		t.Fatal("missing 'configuration' in output")
	}

	cfgMap, ok := cfg.(map[string]interface{})
	if !ok {
		t.Fatal("configuration is not an object")
	}

	if cfgMap["image"] != "ubuntu:latest" {
		t.Errorf("image = %v, want 'ubuntu:latest'", cfgMap["image"])
	}
}

func TestReadConfiguration_DockerfileFixture(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", "dockerfile-without-features")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"read-configuration", "--workspace-folder", fixtureDir})
	root.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	cfg := result["configuration"].(map[string]interface{})
	build, ok := cfg["build"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'build' in Dockerfile config")
	}
	if build["dockerfile"] != "Dockerfile" {
		t.Errorf("dockerfile = %v", build["dockerfile"])
	}
}

func TestReadConfiguration_ComposeFixture(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "src", "test", "configs", "compose-image-without-features")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"read-configuration", "--workspace-folder", fixtureDir})
	root.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	cfg := result["configuration"].(map[string]interface{})
	if cfg["service"] != "app" {
		t.Errorf("service = %v, want 'app'", cfg["service"])
	}
}

func TestReadConfiguration_AllFixtures(t *testing.T) {
	fixturesDir := filepath.Join("..", "..", "src", "test", "configs")
	if _, err := os.Stat(fixturesDir); os.IsNotExist(err) {
		t.Skip("fixtures not found")
	}

	entries, _ := os.ReadDir(fixturesDir)
	var passed, skipped int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(fixturesDir, entry.Name())

		// Check if fixture has a devcontainer.json
		hasConfig := false
		for _, candidate := range []string{
			filepath.Join(dir, ".devcontainer", "devcontainer.json"),
			filepath.Join(dir, ".devcontainer.json"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				hasConfig = true
				break
			}
		}
		if !hasConfig {
			skipped++
			continue
		}

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		root := NewRootCommand()
		root.SetArgs([]string{"read-configuration", "--workspace-folder", dir})
		err := root.Execute()

		w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		buf.ReadFrom(r)

		if err != nil {
			t.Errorf("FAIL %s: %v", entry.Name(), err)
			continue
		}

		output := strings.TrimSpace(buf.String())
		if output == "" {
			t.Errorf("FAIL %s: empty output", entry.Name())
			continue
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Errorf("FAIL %s: invalid JSON: %v", entry.Name(), err)
			continue
		}

		if _, ok := result["configuration"]; !ok {
			t.Errorf("FAIL %s: missing 'configuration'", entry.Name())
			continue
		}

		passed++
	}

	t.Logf("read-configuration: %d passed, %d skipped", passed, skipped)
}

func TestComposeServiceConfig(t *testing.T) {
	cfg := map[string]interface{}{
		"services": map[string]interface{}{
			"app": map[string]interface{}{
				"image": "example/app:latest",
			},
		},
	}

	got := composeServiceConfig(cfg, "app")
	if got == nil {
		t.Fatal("composeServiceConfig() = nil")
	}
	if got["image"] != "example/app:latest" {
		t.Fatalf("image = %v", got["image"])
	}
}

func TestConfigToMetadataEntry_IncludesMountsAndUIDSetting(t *testing.T) {
	trueValue := true
	cfg := &config.DevContainerConfig{
		UpdateRemoteUserUID: &trueValue,
		Mounts: []config.MountOrString{
			mountString("type=volume,target=/data"),
			mountObject(config.Mount{Type: "bind", Source: "/tmp/src", Target: "/workspace"}),
		},
	}

	entry := configToMetadataEntry(cfg)

	if entry.UpdateRemoteUserUID == nil || !*entry.UpdateRemoteUserUID {
		t.Fatal("expected updateRemoteUserUID to be preserved")
	}
	if len(entry.Mounts) != 2 {
		t.Fatalf("mounts len = %d", len(entry.Mounts))
	}
	if entry.Mounts[0] != "type=volume,target=/data" {
		t.Fatalf("mounts[0] = %v", entry.Mounts[0])
	}
	second, ok := entry.Mounts[1].(map[string]interface{})
	if !ok {
		t.Fatalf("mounts[1] type = %T", entry.Mounts[1])
	}
	if second["type"] != "bind" || second["source"] != "/tmp/src" || second["target"] != "/workspace" {
		t.Fatalf("mounts[1] = %#v", second)
	}
}

func TestDockerfileBuildMetadataEntries_PreservesBaseAndConfigMetadata(t *testing.T) {
	cfg := &config.DevContainerConfig{
		PostCreateCommand: lifecycleCommand("touch /tmp/postCreateCommand.testmarker"),
		PostStartCommand:  lifecycleCommand("touch /tmp/postStartCommand.testmarker"),
		PostAttachCommand: lifecycleCommand("touch /tmp/postAttachCommand.testmarker"),
	}

	baseLabel := `[{"remoteUser":"node"}]`
	entries := dockerfileBuildMetadataEntries(cfg, map[string]string{
		"devcontainer.metadata": baseLabel,
	}, log.Null)
	merged := imagemeta.MergeConfiguration(entries)

	if merged.RemoteUser != "node" {
		t.Fatalf("remoteUser = %q, want %q", merged.RemoteUser, "node")
	}
	if len(merged.PostCreateCommands) != 1 {
		t.Fatalf("postCreateCommands len = %d, want 1", len(merged.PostCreateCommands))
	}
	if len(merged.PostStartCommands) != 1 {
		t.Fatalf("postStartCommands len = %d, want 1", len(merged.PostStartCommands))
	}
	if len(merged.PostAttachCommands) != 1 {
		t.Fatalf("postAttachCommands len = %d, want 1", len(merged.PostAttachCommands))
	}
}

func mountString(spec string) config.MountOrString {
	var value config.MountOrString
	payload, _ := json.Marshal(spec)
	_ = value.UnmarshalJSON(payload)
	return value
}

func mountObject(mount config.Mount) config.MountOrString {
	var value config.MountOrString
	payload, _ := json.Marshal(mount)
	_ = value.UnmarshalJSON(payload)
	return value
}

func lifecycleCommand(command string) config.LifecycleCommand {
	var value config.LifecycleCommand
	payload, _ := json.Marshal(command)
	_ = value.UnmarshalJSON(payload)
	return value
}
