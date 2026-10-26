package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/moby/moby/api/types/container"
)

// runSetUpCmd runs `set-up` with args and returns its stdout, stderr and error.
func runSetUpCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newSetUpCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSetUpRejectsInvalidFlagsBeforeLookingForTheContainer(t *testing.T) {
	// No engine is reachable: a flag that passed validation would end in
	// "Dev container not found." on stdout instead.
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no container", nil, "Missing required argument: --container-id"},
		{"log level", []string{"--container-id", "c1", "--log-level", "loud"}, "log-level"},
		{"log format", []string{"--container-id", "c1", "--log-format", "xml"}, "log-format"},
		{"env probe", []string{"--container-id", "c1", "--default-user-env-probe", "always"}, "default-user-env-probe"},
		{"terminal", []string{"--container-id", "c1", "--terminal-columns", "80"}, "terminal-rows"},
		{"remote env", []string{"--container-id", "c1", "--remote-env", "NOEQUALS"}, "remote-env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runSetUpCmd(t, tc.args...)
			if err == nil {
				t.Fatal("set-up accepted invalid flags")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing for a usage error", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.want)
			}
		})
	}
}

func TestSetUpReportsAContainerTheEngineCannotFind(t *testing.T) {
	startFakeEngine(t, map[string]map[string]string{})

	stdout, _, err := runSetUpCmd(t, "--container-id", "missing", "--terminal-columns", "80", "--terminal-rows", "24", "--remote-env", "A=b")

	if err == nil {
		t.Fatal("set-up succeeded without a container")
	}
	var got map[string]any
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a JSON result: %v\n%s", jerr, stdout)
	}
	if got["outcome"] != "error" || got["message"] != "Dev container not found." {
		t.Errorf("result = %v, want Dev container not found.", got)
	}
}

func TestSetUpReportsALogFileItCannotOpen(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	stdout, _, err := runSetUpCmd(t, "--container-id", "c1", "--log-file", t.TempDir())

	if err == nil || !strings.Contains(stdout, `"open log file: `) {
		t.Errorf("stdout = %q, err = %v; want the log file error", stdout, err)
	}
}

func TestSetUpReportsAnUnusableDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "not a docker host")

	stdout, _, err := runSetUpCmd(t, "--container-id", "c1")

	if err == nil || !strings.Contains(stdout, `"Docker engine: `) {
		t.Errorf("stdout = %q, err = %v; want the engine client's error", stdout, err)
	}
}

// TS doSetUp reads --config with no workspace: a file that does not exist is
// "Dev container config (...) not found.", one that does not load is an error,
// and only a config's own workspaceFolder gives ${containerWorkspaceFolder}.
func TestSetUpConfigLoadsTheGivenConfigWithoutAWorkspace(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(good, []byte(`{"image":"alpine:3","remoteUser":"vscode","remoteEnv":{
		"A":"${localWorkspaceFolder}","B":"${localWorkspaceFolderBasename}",
		"C":"${containerWorkspaceFolder}","D":"${containerWorkspaceFolderBasename}"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	withFolder := filepath.Join(dir, "with-folder.json")
	if err := os.WriteFile(withFolder, []byte(`{"image":"alpine:3","workspaceFolder":"/src/app","remoteEnv":{"C":"${containerWorkspaceFolder}","D":"${containerWorkspaceFolderBasename}"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if loaded, err := setUpConfig(""); loaded != nil || err != nil {
		t.Errorf("setUpConfig(\"\") = %+v, %v; want nil without --config", loaded, err)
	}
	loaded, err := setUpConfig(good)
	if err != nil || loaded.Config.RemoteUser != "vscode" {
		t.Fatalf("setUpConfig(%q) = %+v, %v; want the config's remoteUser", good, loaded, err)
	}
	want := map[string]any{"A": "${localWorkspaceFolder}", "B": "${localWorkspaceFolderBasename}", "C": "${containerWorkspaceFolder}", "D": "${containerWorkspaceFolderBasename}"}
	if got := loaded.Raw["remoteEnv"]; !reflect.DeepEqual(got, want) {
		t.Errorf("remoteEnv = %v, want the workspace variables as written %v", got, want)
	}
	loaded, err = setUpConfig(withFolder)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.Raw["remoteEnv"], map[string]any{"C": "/src/app", "D": "app"}; !reflect.DeepEqual(got, want) {
		t.Errorf("remoteEnv = %v, want the config's workspaceFolder %v", got, want)
	}

	missing := filepath.Join(dir, "missing.json")
	_, err = setUpConfig(missing)
	if out := setUpErrorOutput(err); out.Message != "Dev container config ("+missing+") not found." || out.Description != out.Message {
		t.Errorf("missing config: %+v, want TS's not found error", out)
	}
	_, err = setUpConfig(bad)
	if out := setUpErrorOutput(err); out.Message != "Dev container config ("+bad+") must contain a JSON object literal." || out.Description != out.Message {
		t.Errorf("config that is no object: %+v, want TS's error", out)
	}
	if out := setUpErrorOutput(errors.New("boom")); out.Message != "boom" || out.Description != "An error occurred running user commands in the container." {
		t.Errorf("other error: %+v, want TS's wrapped error", out)
	}
}

// set-up reads --config before it looks for the container, as TS doSetUp
// does, so a missing config is reported even when the container is missing.
func TestSetUpReportsAMissingConfigBeforeTheContainer(t *testing.T) {
	startFakeEngine(t, map[string]map[string]string{})
	missing := filepath.Join(t.TempDir(), "nope.json")

	stdout, _, err := runSetUpCmd(t, "--container-id", "missing", "--config", missing)

	if err == nil {
		t.Fatal("set-up succeeded without its config")
	}
	var got map[string]any
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a JSON result: %v\n%s", jerr, stdout)
	}
	if want := "Dev container config (" + missing + ") not found."; got["outcome"] != "error" || got["message"] != want || got["description"] != want {
		t.Errorf("result = %v, want %q", got, want)
	}
}

func TestSetUpMergedPutsTheConfigOverTheContainerMetadata(t *testing.T) {
	labels := map[string]string{imagemeta.MetadataLabel: `[{"remoteUser":"node","postCreateCommand":"from-image"}]`}
	logger := log.New(log.Options{Writer: &bytes.Buffer{}})
	cfg := &config.DevContainer{RemoteUser: "vscode"}
	if err := json.Unmarshal([]byte(`"from-config"`), &cfg.PostCreateCommand); err != nil {
		t.Fatal(err)
	}

	if got := imagemeta.MergeConfiguration(setUpEntries(labels, nil, logger)); got.RemoteUser != "node" || !reflect.DeepEqual(got.PostCreateCommands, []any{"from-image"}) {
		t.Errorf("without a config: remoteUser %q, postCreate %v; want the label's", got.RemoteUser, got.PostCreateCommands)
	}
	got := imagemeta.MergeConfiguration(setUpEntries(labels, cfg, logger))
	if got.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q, want the config's", got.RemoteUser)
	}
	if want := []any{"from-image", "from-config"}; !reflect.DeepEqual(got.PostCreateCommands, want) {
		t.Errorf("postCreateCommands = %v, want %v", got.PostCreateCommands, want)
	}
}

// setUpResultJSON is setUpResult's result as set-up prints it.
func setUpResultJSON(t *testing.T, loaded *config.LoadResult, entries []imagemeta.Entry, env []string, includeConfig, includeMerged bool) map[string]any {
	t.Helper()
	result, err := setUpResult(loaded, entries, &container.Config{Env: env}, includeConfig, includeMerged)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func strPtr(s string) *string { return &s }

// loadedConfig is the LoadResult of a config with the given raw properties,
// read from configFile.
func loadedConfig(t *testing.T, configFile string, raw map[string]any) *config.LoadResult {
	t.Helper()
	var cfg config.DevContainer
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ConfigFilePath = configFile
	return &config.LoadResult{Config: &cfg, Raw: raw}
}

func TestSetUpResultReportsTheConfigurationAsTSDoes(t *testing.T) {
	loaded := loadedConfig(t, "/ws/.devcontainer/devcontainer.json", map[string]any{
		"image":     "alpine:3",
		"remoteEnv": map[string]any{"FROM_CONTAINER": "${containerEnv:FOO}"},
		// Properties the typed config has no field for, or leaves behind,
		// are reported as written, as TS reports its configuration.
		"foo":        map[string]any{"b": 1.0},
		"extensions": "",
	})
	entries := setUpEntries(nil, loaded.Config, log.Null)

	got := setUpResultJSON(t, loaded, entries, []string{"FOO=bar"}, true, true)

	wantURI := map[string]any{"$mid": float64(1), "fsPath": loaded.Config.ConfigFilePath, "path": loaded.Config.ConfigFilePath, "scheme": "file"}
	conf, _ := got["configuration"].(map[string]any)
	mc, _ := got["mergedConfiguration"].(map[string]any)
	for name, m := range map[string]map[string]any{"configuration": conf, "mergedConfiguration": mc} {
		if !reflect.DeepEqual(m["configFilePath"], wantURI) {
			t.Errorf("%s.configFilePath = %v, want %v", name, m["configFilePath"], wantURI)
		}
		if re, _ := m["remoteEnv"].(map[string]any); re["FROM_CONTAINER"] != "bar" {
			t.Errorf("%s.remoteEnv = %v, want ${containerEnv:FOO} resolved to bar", name, m["remoteEnv"])
		}
		if m["image"] != "alpine:3" || !reflect.DeepEqual(m["foo"], map[string]any{"b": 1.0}) || m["extensions"] != "" {
			t.Errorf("%s = %v, want image, foo and extensions as written", name, m)
		}
		for k, v := range m {
			if v == nil {
				t.Errorf("%s.%s is null; TS leaves unset properties out", name, k)
			}
		}
	}
}

func TestSetUpResultLeavesOutWhatWasNotAskedFor(t *testing.T) {
	if got := setUpResultJSON(t, nil, nil, nil, false, false); !reflect.DeepEqual(got, map[string]any{"outcome": "success"}) {
		t.Errorf("result = %v, want the outcome alone", got)
	}
	got := setUpResultJSON(t, nil, nil, nil, true, false)
	if !reflect.DeepEqual(got["configuration"], map[string]any{}) {
		t.Errorf("configuration without a config = %v, want {}", got["configuration"])
	}
	if _, ok := got["mergedConfiguration"]; ok {
		t.Errorf("mergedConfiguration reported without --include-merged-configuration: %v", got)
	}
}

// TS substitutes no URI (substitute0), so configFilePath is the file's path
// as it is, whatever ${containerEnv:...} it reads like.
func TestSetUpResultLeavesTheConfigFilePathUnsubstituted(t *testing.T) {
	file := "/ws/${containerEnv:UNSET}/devcontainer.json"
	loaded := loadedConfig(t, file, map[string]any{"image": "alpine:3"})

	got := setUpResultJSON(t, loaded, nil, nil, true, true)

	for _, name := range []string{"configuration", "mergedConfiguration"} {
		m, _ := got[name].(map[string]any)
		if uri, _ := m["configFilePath"].(map[string]any); uri["fsPath"] != file || uri["path"] != file {
			t.Errorf("%s.configFilePath = %v, want the path %q", name, m["configFilePath"], file)
		}
	}
}

func TestSetUpResultReportsAContainerEnvReferenceWithoutAName(t *testing.T) {
	loaded := loadedConfig(t, "/ws/devcontainer.json", map[string]any{"remoteEnv": map[string]any{"X": "${containerEnv}"}})
	if _, err := setUpResult(loaded, nil, &container.Config{}, true, false); err == nil || !strings.HasPrefix(err.Error(), "resolve configuration container variables: ") {
		t.Errorf("configuration error = %v, want the substitution error", err)
	}
	entries := []imagemeta.Entry{{RemoteEnv: map[string]*string{"X": strPtr("${containerEnv}")}}}
	if _, err := setUpResult(nil, entries, &container.Config{}, false, true); err == nil || !strings.HasPrefix(err.Error(), "resolve merged container variables: ") {
		t.Errorf("merged error = %v, want the substitution error", err)
	}
}
