package cli

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

func TestReadConfigurationWritesTSKeyOrder(t *testing.T) {
	startFakeEngine(t, nil)
	files := map[string]string{".devcontainer/devcontainer.json": `{
		// a comment
		"name": "ko",
		"image": "alpine:3",
		"remoteUser": "root",
		"features": {"./feat": {}},
		"containerEnv": {"Z": "1"},
		"postCreateCommand": "true",
		"forwardPorts": [3000],
		"capAdd": ["SYS_PTRACE"],
	}`}
	for k, v := range localFeatureFiles {
		files[k] = v
	}
	stdout, err := readConfigurationStdout("--workspace-folder", writeConfigFixture(t, files), "--include-features-configuration")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]string{
		"":                      {"configuration", "workspace", "featuresConfiguration", "ociAuthDiagnostics"},
		"configuration":         {"name", "image", "remoteUser", "features", "containerEnv", "postCreateCommand", "forwardPorts", "capAdd", "configFilePath"},
		"featuresConfiguration": {"featureSets", "dstFolder"},
	} {
		if got, err := jsonKeyOrder(stdout, path); err != nil || !slices.Equal(got, want) {
			t.Errorf("keys of %q = %v (%v), want %v", path, got, err, want)
		}
	}

	stdout, err = readConfigurationStdout("--workspace-folder", writeConfigFixture(t, map[string]string{
		".devcontainer.json": `{"image": "alpine:3", "extensions": ["a.b"], "name": "d", "forwardPorts": [1]}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"image", "name", "forwardPorts", "customizations", "configFilePath"}
	if got, err := jsonKeyOrder(stdout, "configuration"); err != nil || !slices.Equal(got, want) {
		t.Errorf("configuration with a deprecated property: keys = %v (%v), want %v", got, err, want)
	}
}

func TestReadConfigurationWritesNestedObjectsInPropertyOrder(t *testing.T) {
	startFakeEngine(t, nil)
	stdout, err := readConfigurationStdout("--workspace-folder", writeConfigFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{
			"image": "alpine:3",
			"containerEnv": {"ZED": "1", "10": "2", "ALPHA": "3", "2": "4"},
			"features": {"zeta": {"zz": "1", "10": "t", "a": "b", "2": "s"}, "10": {}, "alpha": {}}
		}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]string{
		"configuration.containerEnv":  {"2", "10", "ZED", "ALPHA"},
		"configuration.features":      {"10", "zeta", "alpha"},
		"configuration.features.zeta": {"2", "10", "zz", "a"},
	} {
		if got, err := jsonKeyOrder(stdout, path); err != nil || !slices.Equal(got, want) {
			t.Errorf("keys of %q = %v (%v), want %v", path, got, err, want)
		}
	}
}

func TestOrderConfigurationsWritesTSMergedOrder(t *testing.T) {
	loaded := &config.LoadResult{KeyOrder: []string{"name", "customizations", "image", "remoteUser", "postCreateCommand", "containerEnv"}}
	fields := func(keys ...string) map[string]any {
		m := map[string]any{}
		for _, k := range keys {
			m[k] = 1
		}
		return m
	}
	result := map[string]any{
		"outcome":       "success",
		"configuration": fields("name", "customizations", "image", "remoteUser", "postCreateCommand", "containerEnv", "configFilePath"),
		"mergedConfiguration": fields("name", "image", "remoteUser", "containerEnv", "configFilePath", "init", "privileged",
			"onCreateCommands", "updateContentCommands", "postCreateCommands", "postStartCommands", "postAttachCommands",
			"remoteEnv", "portsAttributes", "customizations", "zGoOnly", "aGoOnly"),
	}
	orderConfigurations(result, loaded)
	data, err := json.Marshal(orderedObject{result, setUpResultKeys})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]string{
		"":              {"outcome", "configuration", "mergedConfiguration"},
		"configuration": {"name", "customizations", "image", "remoteUser", "postCreateCommand", "containerEnv", "configFilePath"},
		"mergedConfiguration": {"name", "image", "remoteUser", "containerEnv", "configFilePath", "init", "privileged",
			"customizations", "onCreateCommands", "updateContentCommands", "postCreateCommands", "postStartCommands",
			"postAttachCommands", "remoteEnv", "portsAttributes", "aGoOnly", "zGoOnly"},
	} {
		if got, err := jsonKeyOrder(string(data), path); err != nil || !slices.Equal(got, want) {
			t.Errorf("keys of %q = %v (%v), want %v", path, got, err, want)
		}
	}
}

func TestOrderedObjectReportsAValueItCannotWrite(t *testing.T) {
	if _, err := json.Marshal(orderedObject{map[string]any{"x": make(chan int)}, nil}); err == nil {
		t.Error("a channel value was written")
	}
	out := &captureOutput{}
	if err := writeSuccessJSON(out, map[string]any{"x": func() {}}); err == nil || out.out.Len() != 0 {
		t.Errorf("writeSuccessJSON = %v, stdout %q", err, out.out.String())
	}
}
