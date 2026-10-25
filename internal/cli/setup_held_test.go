package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
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

func TestSetUpConfigLoadsTheGivenConfigOnly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(good, []byte(`{"image":"alpine:3","remoteUser":"vscode"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte(`{"image":`), 0o644); err != nil {
		t.Fatal(err)
	}

	if cfg := setUpConfig(""); cfg != nil {
		t.Errorf("setUpConfig(\"\") = %+v, want nil without --config", cfg)
	}
	if cfg := setUpConfig(good); cfg == nil || cfg.RemoteUser != "vscode" {
		t.Errorf("setUpConfig(%q) = %+v, want the config's remoteUser", good, cfg)
	}
	if cfg := setUpConfig(bad); cfg != nil {
		t.Errorf("setUpConfig(%q) = %+v, want nil for a config that does not load", bad, cfg)
	}
}

func TestSetUpMergedPutsTheConfigOverTheContainerMetadata(t *testing.T) {
	labels := map[string]string{imagemeta.MetadataLabel: `[{"remoteUser":"node","postCreateCommand":"from-image"}]`}
	logger := log.New(log.Options{Writer: &bytes.Buffer{}})
	cfg := &config.DevContainer{RemoteUser: "vscode"}
	if err := json.Unmarshal([]byte(`"from-config"`), &cfg.PostCreateCommand); err != nil {
		t.Fatal(err)
	}

	if got := setUpMerged(labels, nil, logger); got.RemoteUser != "node" || !reflect.DeepEqual(got.PostCreateCommands, []any{"from-image"}) {
		t.Errorf("without a config: remoteUser %q, postCreate %v; want the label's", got.RemoteUser, got.PostCreateCommands)
	}
	got := setUpMerged(labels, cfg, logger)
	if got.RemoteUser != "vscode" {
		t.Errorf("remoteUser = %q, want the config's", got.RemoteUser)
	}
	if want := []any{"from-image", "from-config"}; !reflect.DeepEqual(got.PostCreateCommands, want) {
		t.Errorf("postCreateCommands = %v, want %v", got.PostCreateCommands, want)
	}
}

// setUpResultJSON is setUpResult's result as set-up prints it.
func setUpResultJSON(t *testing.T, cfg *config.DevContainer, merged *imagemeta.MergedConfig, env map[string]string, includeConfig, includeMerged bool) map[string]any {
	t.Helper()
	result, err := setUpResult(cfg, merged, env, includeConfig, includeMerged)
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

func TestSetUpResultReportsTheConfigurationAsTSDoes(t *testing.T) {
	cfg := &config.DevContainer{
		ConfigFilePath: "/ws/.devcontainer/devcontainer.json",
		Image:          "alpine:3",
		RemoteEnv:      map[string]*string{"FROM_CONTAINER": strPtr("${containerEnv:FOO}")},
	}
	merged := imagemeta.MergeConfiguration([]imagemeta.Entry{configToMetadataEntry(cfg)})
	env := map[string]string{"FOO": "bar"}

	got := setUpResultJSON(t, cfg, merged, env, true, true)

	wantURI := map[string]any{"$mid": float64(1), "fsPath": cfg.ConfigFilePath, "path": cfg.ConfigFilePath, "scheme": "file"}
	conf, _ := got["configuration"].(map[string]any)
	if !reflect.DeepEqual(conf["configFilePath"], wantURI) {
		t.Errorf("configuration.configFilePath = %v, want %v", conf["configFilePath"], wantURI)
	}
	if re, _ := conf["remoteEnv"].(map[string]any); re["FROM_CONTAINER"] != "bar" {
		t.Errorf("configuration.remoteEnv = %v, want ${containerEnv:FOO} resolved to bar", conf["remoteEnv"])
	}
	for k, v := range conf {
		if v == nil {
			t.Errorf("configuration.%s is null; TS leaves unset properties out", k)
		}
	}
	mc, _ := got["mergedConfiguration"].(map[string]any)
	if !reflect.DeepEqual(mc["configFilePath"], wantURI) {
		t.Errorf("mergedConfiguration.configFilePath = %v, want %v", mc["configFilePath"], wantURI)
	}
	if re, _ := mc["remoteEnv"].(map[string]any); re["FROM_CONTAINER"] != "bar" {
		t.Errorf("mergedConfiguration.remoteEnv = %v, want ${containerEnv:FOO} resolved to bar", mc["remoteEnv"])
	}
}

func TestSetUpResultLeavesOutWhatWasNotAskedFor(t *testing.T) {
	merged := imagemeta.MergeConfiguration(nil)

	if got := setUpResultJSON(t, nil, merged, nil, false, false); !reflect.DeepEqual(got, map[string]any{"outcome": "success"}) {
		t.Errorf("result = %v, want the outcome alone", got)
	}
	got := setUpResultJSON(t, nil, merged, nil, true, false)
	if !reflect.DeepEqual(got["configuration"], map[string]any{}) {
		t.Errorf("configuration without a config = %v, want {}", got["configuration"])
	}
	if _, ok := got["mergedConfiguration"]; ok {
		t.Errorf("mergedConfiguration reported without --include-merged-configuration: %v", got)
	}
}

func TestSetUpResultDropsAConfigFilePathThatResolvesToNothing(t *testing.T) {
	cfg := &config.DevContainer{ConfigFilePath: "${containerEnv:UNSET}"}

	got := setUpResultJSON(t, cfg, imagemeta.MergeConfiguration(nil), nil, true, true)

	conf, _ := got["configuration"].(map[string]any)
	if v, ok := conf["configFilePath"]; ok {
		t.Errorf("configuration.configFilePath = %v, want it left out", v)
	}
	mc, _ := got["mergedConfiguration"].(map[string]any)
	if v, ok := mc["configFilePath"]; ok {
		t.Errorf("mergedConfiguration.configFilePath = %v, want it left out", v)
	}
}

func TestSetUpResultReportsAContainerEnvReferenceWithoutAName(t *testing.T) {
	cfg := &config.DevContainer{RemoteEnv: map[string]*string{"X": strPtr("${containerEnv}")}}
	if _, err := setUpResult(cfg, imagemeta.MergeConfiguration(nil), nil, true, false); err == nil || !strings.HasPrefix(err.Error(), "resolve configuration container variables: ") {
		t.Errorf("configuration error = %v, want the substitution error", err)
	}
	merged := imagemeta.MergeConfiguration([]imagemeta.Entry{{RemoteEnv: cfg.RemoteEnv}})
	if _, err := setUpResult(nil, merged, nil, false, true); err == nil || !strings.HasPrefix(err.Error(), "resolve merged container variables: ") {
		t.Errorf("merged error = %v, want the substitution error", err)
	}
}
