package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

// loadFixtureConfig writes a workspace with .devcontainer/devcontainer.json
// (and the other files given) and loads it as read-configuration does.
func loadFixtureConfig(t *testing.T, devcontainerJSON string, files map[string]string) (string, *config.LoadResult) {
	t.Helper()
	all := map[string]string{".devcontainer/devcontainer.json": devcontainerJSON}
	for k, v := range files {
		all[k] = v
	}
	ws := writeConfigFixture(t, all)
	result, err := loadReadConfiguration(ws, "", "", &readConfigOpts{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return ws, result
}

func TestLoadReadConfiguration(t *testing.T) {
	_, err := loadReadConfiguration(t.TempDir(), "", "", &readConfigOpts{})
	wantExitCode(t, err, 1)

	ws := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{
		"image": "img:1",
		"extensions": ["ext.one"],
		"settings": {"s": 1},
		"devPort": 3000
	}`})
	result, err := loadReadConfiguration(ws, "", "", &readConfigOpts{additionalFeatures: `{"ghcr.io/x/y:1": {}}`})
	if err != nil {
		t.Fatal(err)
	}
	if c := result.Config; c.Extensions != nil || c.Settings != nil || c.DevPort != nil {
		t.Errorf("legacy properties = %v, %v, %v; want them dropped", c.Extensions, c.Settings, c.DevPort)
	}
	if _, ok := result.Config.Features["ghcr.io/x/y:1"]; !ok {
		t.Errorf("features = %v, want the additional Feature", result.Config.Features)
	}
	if _, err := loadReadConfiguration(ws, "", "", &readConfigOpts{additionalFeatures: `not json`}); err == nil || !strings.HasPrefix(err.Error(), "additional-features: ") {
		t.Errorf("invalid additional features: err = %v", err)
	}
	broken := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image": `})
	if _, err := loadReadConfiguration(broken, "", "", &readConfigOpts{}); err == nil {
		t.Error("unparseable config: want an error")
	} else if _, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok {
		t.Errorf("unparseable config: err = %v, want the parse error, not a silent exit", err)
	}
}

func TestReadConfigurationPaths(t *testing.T) {
	const cwd = "/work"
	for _, tc := range []struct {
		name                string
		opts                readConfigOpts
		ws, config, overrid string
	}{
		{"defaults to cwd", readConfigOpts{}, "/work", "", ""},
		{"relative paths", readConfigOpts{workspaceFolder: "ws", configPath: "c.json", overrideConfig: "o.json"}, "/work/ws", "/work/c.json", "/work/o.json"},
		{"absolute paths", readConfigOpts{workspaceFolder: "/ws", configPath: "/c.json", overrideConfig: "/o.json"}, "/ws", "/c.json", "/o.json"},
		{"container id, no workspace", readConfigOpts{containerID: "abc"}, "", "", ""},
		{"id label, no workspace", readConfigOpts{idLabels: []string{"a=b"}}, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, cfg, override := readConfigurationPaths(&tc.opts, cwd)
			if ws != tc.ws || cfg != tc.config || override != tc.overrid {
				t.Fatalf("paths = %q, %q, %q; want %q, %q, %q", ws, cfg, override, tc.ws, tc.config, tc.overrid)
			}
		})
	}
}

func TestConfigurationOutput(t *testing.T) {
	if got := configurationOutput(nil, &readConfigOpts{}); len(got) != 0 || got == nil {
		t.Fatalf("without a config: %v, want an empty object", got)
	}

	ws, result := loadFixtureConfig(t, `{
		"name": "${localWorkspaceFolderBasename}",
		"image": "img:1",
		"x-vendor": {"a": 1},
		"extensions": ["ext.one"],
		"settings": {"s": 1},
		"devPort": 3000
	}`, nil)
	got := configurationOutput(result, &readConfigOpts{})
	if got["name"] != filepath.Base(ws) {
		t.Errorf("name = %v, want the substituted %q", got["name"], filepath.Base(ws))
	}
	if !reflect.DeepEqual(got["x-vendor"], map[string]any{"a": float64(1)}) {
		t.Errorf("x-vendor = %v, want the unknown property kept", got["x-vendor"])
	}
	for _, k := range []string{"extensions", "settings", "devPort", "onCreateCommand"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %v, want it left out", k, v)
		}
	}
	vscode, _ := got["customizations"].(map[string]any)["vscode"].(map[string]any)
	if !reflect.DeepEqual(vscode["extensions"], []any{"ext.one"}) {
		t.Errorf("customizations.vscode = %v, want the migrated extensions", vscode)
	}
	cfp := result.Config.ConfigFilePath
	if want := map[string]any{"$mid": 1, "fsPath": cfp, "path": cfp, "scheme": "vscode-fileHost"}; !reflect.DeepEqual(got["configFilePath"], want) {
		t.Errorf("configFilePath = %v, want %v", got["configFilePath"], want)
	}

	for name, opts := range map[string]readConfigOpts{
		"--config":          {configPath: cfp},
		"--override-config": {overrideConfig: cfp},
		"--container-id":    {containerID: "abc"},
	} {
		got := configurationOutput(result, &opts)
		if scheme := got["configFilePath"].(map[string]any)["scheme"]; scheme != "file" {
			t.Errorf("with %s: scheme = %v, want file", name, scheme)
		}
	}
}

func TestReadConfigurationContainerID(t *testing.T) {
	ctx := context.Background()
	_, result := loadFixtureConfig(t, `{"image": "img"}`, nil)
	ws := "/some/workspace"
	configLabel := "devcontainer.config_file=" + result.Config.ConfigFilePath
	api := &fakeEngineAPI{list: func(labels []string) []string {
		if slices.Contains(labels, configLabel) {
			return []string{"by-config"}
		}
		if slices.Contains(labels, "a=b") {
			return []string{"by-label"}
		}
		return nil
	}}
	engine := &docker.EngineClient{API: api, Log: log.Null}

	if got := readConfigurationContainerID(ctx, engine, &readConfigOpts{}, ws, result); got != "by-config" {
		t.Errorf("by workspace and config file = %q, want by-config", got)
	}
	if got := readConfigurationContainerID(ctx, engine, &readConfigOpts{idLabels: []string{"a=b"}}, "", nil); got != "by-label" {
		t.Errorf("by id label = %q, want by-label", got)
	}
	api.listed = nil
	if got := readConfigurationContainerID(ctx, engine, &readConfigOpts{}, ws, nil); got != "" {
		t.Errorf("without a config = %q, want none", got)
	}
	if len(api.listed) != 1 || slices.ContainsFunc(api.listed[0], func(l string) bool { return strings.HasPrefix(l, "devcontainer.config_file=") }) {
		t.Errorf("without a config, listed by %v; want the local folder alone", api.listed)
	}
	api.listed = nil
	if got := readConfigurationContainerID(ctx, engine, &readConfigOpts{containerID: "given"}, ws, result); got != "given" || len(api.listed) != 0 {
		t.Errorf("--container-id = %q (listed %v), want it as given without a lookup", got, api.listed)
	}
	if got := readConfigurationContainerID(ctx, nil, &readConfigOpts{}, ws, result); got != "" {
		t.Errorf("without an engine = %q, want none", got)
	}
	if got := readConfigurationContainerID(ctx, nil, &readConfigOpts{containerID: "given"}, ws, result); got != "given" {
		t.Errorf("--container-id without an engine = %q, want given", got)
	}
}

// A container read-configuration cannot inspect fails it, as TS's
// inspectContainer throws; with no container there is nothing to inspect.
func TestInspectContainerConfig(t *testing.T) {
	ctx := context.Background()
	api := &fakeEngineAPI{containers: map[string]string{
		"c1":       `{"Id":"c1","Config":{"Env":["A=b"]}}`,
		"noconfig": `{"Id":"noconfig"}`,
	}}
	engine := &docker.EngineClient{API: api, Log: log.Null}
	if got, err := inspectContainerConfig(ctx, engine, "c1"); err != nil || got == nil || !slices.Equal(got.Env, []string{"A=b"}) {
		t.Errorf("c1 = %+v, %v; want its config", got, err)
	}
	for name, cid := range map[string]string{"no container": "", "container w/o config": "noconfig"} {
		if got, err := inspectContainerConfig(ctx, engine, cid); got != nil || err != nil {
			t.Errorf("%s = %+v, %v; want nil", name, got, err)
		}
	}
	if _, err := inspectContainerConfig(ctx, engine, "missing"); err == nil || !strings.Contains(err.Error(), "inspect container missing") {
		t.Errorf("missing container: err = %v, want the inspect error", err)
	}
	if _, err := inspectContainerConfig(ctx, nil, "c1"); err == nil {
		t.Error("no engine: want an error")
	}
}

func TestSubstituteContainerEnv(t *testing.T) {
	cfgMap := map[string]any{"remoteEnv": map[string]any{"P": "${containerEnv:PATH}"}}
	got, err := substituteContainerEnv(nil, cfgMap)
	if err != nil || !reflect.DeepEqual(got, cfgMap) {
		t.Fatalf("without a container = %v, %v; want the config unchanged", got, err)
	}
	got, err = substituteContainerEnv(&container.Config{Env: []string{"PATH=/bin"}}, cfgMap)
	if err != nil || !reflect.DeepEqual(got, map[string]any{"remoteEnv": map[string]any{"P": "/bin"}}) {
		t.Fatalf("with a container = %v, %v; want ${containerEnv:PATH} resolved", got, err)
	}
}

func TestWorkspaceOutput(t *testing.T) {
	withMount := &config.LoadResult{WorkspaceConfig: &config.WorkspaceConfig{WorkspaceFolder: "/workspaces/p", WorkspaceMount: "type=bind,source=/p,target=/workspaces/p"}}
	withoutMount := &config.LoadResult{WorkspaceConfig: &config.WorkspaceConfig{WorkspaceFolder: "/workspaces/p"}}
	for _, tc := range []struct {
		name          string
		result        *config.LoadResult
		ws, container string
		want          map[string]any
		ok            bool
	}{
		{"no config", nil, "/p", "", nil, false},
		{"no workspace config", &config.LoadResult{}, "/p", "", nil, false},
		{"folder and mount", withMount, "/p", "", map[string]any{"workspaceFolder": "/workspaces/p", "workspaceMount": "type=bind,source=/p,target=/workspaces/p"}, true},
		{"folder only", withoutMount, "/p", "", map[string]any{"workspaceFolder": "/workspaces/p"}, true},
		{"container id without workspace folder", withMount, "", "abc", map[string]any{}, true},
		{"container id and workspace folder", withoutMount, "/p", "abc", map[string]any{"workspaceFolder": "/workspaces/p"}, true},
		{"id label without workspace folder", withoutMount, "", "", map[string]any{"workspaceFolder": "/workspaces/p"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := workspaceOutput(tc.result, tc.ws, tc.container)
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("workspaceOutput = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestNeedsFeaturesConfiguration(t *testing.T) {
	withFeatures := &config.LoadResult{Config: &config.DevContainer{Features: map[string]any{"./f": map[string]any{}}}}
	noFeatures := &config.LoadResult{Config: &config.DevContainer{}}
	for _, tc := range []struct {
		name      string
		opts      readConfigOpts
		container string
		result    *config.LoadResult
		want      bool
	}{
		{"features configuration", readConfigOpts{includeFeaturesCfg: true}, "", withFeatures, true},
		{"features configuration with a container", readConfigOpts{includeFeaturesCfg: true}, "c", withFeatures, true},
		{"merged without a container", readConfigOpts{includeMergedCfg: true}, "", withFeatures, true},
		{"merged with a container", readConfigOpts{includeMergedCfg: true}, "c", withFeatures, false},
		{"neither", readConfigOpts{}, "", withFeatures, false},
		{"no features", readConfigOpts{includeFeaturesCfg: true, includeMergedCfg: true}, "", noFeatures, false},
		{"no config", readConfigOpts{includeFeaturesCfg: true, includeMergedCfg: true}, "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsFeaturesConfiguration(&tc.opts, tc.container, tc.result); got != tc.want {
				t.Fatalf("needsFeaturesConfiguration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergedConfigurationOutput(t *testing.T) {
	_, result := loadFixtureConfig(t, `{
		"image": "img:1",
		"remoteUser": "config-user",
		"remoteEnv": {"P": "${containerEnv:PATH}"},
		"postStartCommand": "from-config",
		"x-vendor": true
	}`, nil)
	cfgMap := configurationOutput(result, &readConfigOpts{})
	base := []imagemeta.Entry{{RemoteUser: "image-user", ContainerUser: "image-container-user"}}
	merge := func(t *testing.T, base, features []imagemeta.Entry, cc *container.Config, result *config.LoadResult, idLabels []string, cfgMap map[string]any) map[string]any {
		t.Helper()
		entries, err := readConfigurationMetadata(base, features, cc, result, idLabels, "/ws", log.Null)
		if err != nil {
			t.Fatal(err)
		}
		got, err := mergedConfigurationOutput(entries, cc, cfgMap)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("without a container", func(t *testing.T) {
		got := merge(t, base, nil, nil, result, nil, cfgMap)
		want := map[string]any{"remoteUser": "config-user", "containerUser": "image-container-user", "image": "img:1", "x-vendor": true, "configFilePath": cfgMap["configFilePath"]}
		for k, v := range want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s = %v, want %v", k, got[k], v)
			}
		}
		if env := got["remoteEnv"]; !reflect.DeepEqual(env, map[string]any{"P": "${containerEnv:PATH}"}) {
			t.Errorf("remoteEnv = %v, want it unresolved without a container", env)
		}
	})

	t.Run("with a container", func(t *testing.T) {
		cc := &container.Config{
			Env:    []string{"PATH=/container/bin"},
			Labels: map[string]string{"devcontainer.metadata": `[{"containerUser":"label-user","postStartCommand":"from-label","containerEnv":{"H":"${localEnv:HOME}"}}]`},
		}
		got := merge(t, nil, []imagemeta.Entry{{ID: "feat", PostStartCommand: "from-feature"}}, cc, result, nil, cfgMap)
		if got["containerUser"] != "label-user" || got["remoteUser"] != "config-user" {
			t.Errorf("users = %v, %v; want the label's containerUser and the config's remoteUser", got["containerUser"], got["remoteUser"])
		}
		if !reflect.DeepEqual(got["postStartCommands"], []any{"from-label", "from-feature", "from-config"}) {
			t.Errorf("postStartCommands = %v, want the label's, the Features' and the config's", got["postStartCommands"])
		}
		if env := got["remoteEnv"]; !reflect.DeepEqual(env, map[string]any{"P": "/container/bin"}) {
			t.Errorf("remoteEnv = %v, want ${containerEnv:PATH} resolved with the container's env", env)
		}
		if env, _ := got["containerEnv"].(map[string]any); env["H"] != result.HostSub.Env["HOME"] {
			t.Errorf("containerEnv = %v, want the label's ${localEnv:HOME} substituted", got["containerEnv"])
		}
	})

	t.Run("with a container the CLI created", func(t *testing.T) {
		// Its label records the configuration's entry: only the configuration's
		// updateable properties follow, so its commands are not merged twice.
		cc := &container.Config{Labels: map[string]string{
			"devcontainer.metadata":     `[{"postStartCommand":"from-config"}]`,
			"devcontainer.local_folder": "/ws",
		}}
		got := merge(t, nil, nil, cc, result, nil, cfgMap)
		if !reflect.DeepEqual(got["postStartCommands"], []any{"from-config"}) || got["remoteUser"] != "config-user" {
			t.Errorf("merged = %v, want the label's command once and the config's remoteUser", got)
		}
	})

	t.Run("without a config", func(t *testing.T) {
		got := merge(t, base, nil, nil, nil, nil, map[string]any{})
		if got["remoteUser"] != "image-user" {
			t.Fatalf("got %v; want the base entries alone", got)
		}
	})
}

func TestConfigMetadataEntry(t *testing.T) {
	var cfg config.DevContainer
	if err := json.Unmarshal([]byte(`{
		"portsAttributes": {"3000": {"label": "app"}},
		"otherPortsAttributes": {"onAutoForward": "ignore"},
		"hostRequirements": {"memory": "4gb"},
		"remoteEnv": {"R": "v"},
		"onCreateCommand": "on-create",
		"updateContentCommand": ["update", "content"],
		"postCreateCommand": "post-create",
		"postStartCommand": "post-start",
		"postAttachCommand": {"a": "post-attach"}
	}`), &cfg); err != nil {
		t.Fatal(err)
	}
	entryJSON := func(e imagemeta.Entry) map[string]any {
		t.Helper()
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	want := map[string]any{
		"portsAttributes":      map[string]any{"3000": map[string]any{"label": "app"}},
		"otherPortsAttributes": map[string]any{"onAutoForward": "ignore"},
		"hostRequirements":     map[string]any{"memory": "4gb"},
		"remoteEnv":            map[string]any{"R": "v"},
		"onCreateCommand":      "on-create",
		"updateContentCommand": []any{"update", "content"},
		"postCreateCommand":    "post-create",
		"postStartCommand":     "post-start",
		"postAttachCommand":    map[string]any{"a": "post-attach"},
	}
	if got := entryJSON(configMetadataEntry(&cfg, false)); !reflect.DeepEqual(got, want) {
		t.Errorf("entry = %v\nwant    %v", got, want)
	}
	delete(want, "remoteEnv")
	if got := entryJSON(configMetadataEntry(&cfg, true)); !reflect.DeepEqual(got, want) {
		t.Errorf("entry omitting remoteEnv = %v\nwant    %v", got, want)
	}
	if got := entryJSON(configMetadataEntry(&config.DevContainer{}, false)); len(got) != 0 {
		t.Errorf("entry of an empty config = %v, want empty", got)
	}
}

// runReadConfigurationCmd runs read-configuration with args and returns its
// stdout parsed, or the command's error.
func runReadConfigurationCmd(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	stdout, err := readConfigurationStdout(args...)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	return out, nil
}

// readConfigurationStdout runs read-configuration with args and returns its
// stdout as written, or the command's error.
func readConfigurationStdout(args ...string) (string, error) {
	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"read-configuration"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func wantExitCode(t *testing.T, err error, code int) {
	t.Helper()
	exitErr, ok := errors.AsType[*coreerrors.ExitCodeError](err)
	if !ok || exitErr.Code != code {
		t.Fatalf("err = %v, want exit code %d", err, code)
	}
}

// localFeatureFiles is a local Feature at .devcontainer/feat whose metadata
// adds a capability.
var localFeatureFiles = map[string]string{
	".devcontainer/feat/devcontainer-feature.json": `{"id": "feat", "version": "1.0.0", "capAdd": ["SYS_PTRACE"]}`,
	".devcontainer/feat/install.sh":                "#!/bin/sh\n",
}

func TestReadConfigurationCommandWithoutConfigExitsOne(t *testing.T) {
	_, err := runReadConfigurationCmd(t, "--workspace-folder", t.TempDir())
	wantExitCode(t, err, 1)
}

func TestReadConfigurationCommandContainerIDWithoutWorkspace(t *testing.T) {
	// --container-id alone loads no configuration; a container that cannot be
	// inspected fails the command with no result, as TS's inspectContainer throws.
	_, err := runReadConfigurationCmd(t, "--container-id", "devcontainer-cli-test-no-such-container")
	wantExitCode(t, err, 1)
	if !strings.Contains(err.Error(), "inspect container devcontainer-cli-test-no-such-container") {
		t.Errorf("err = %v, want the inspect failure (no configuration is loaded)", err)
	}
}

// --cache-key over a malformed devcontainer-lock.json is a runtime failure:
// the error envelope on stdout naming the lockfile, exit 1. Without the flag
// the command does not read the lockfile, as the reference's
// read-configuration does not, and succeeds.
func TestReadConfigurationCacheKeyFailsOnAMalformedLockfile(t *testing.T) {
	ws := writeConfigFixture(t, map[string]string{
		".devcontainer/devcontainer.json":      `{"image": "img:1"}`,
		".devcontainer/devcontainer-lock.json": `not json`,
	})
	if _, err := runReadConfigurationCmd(t, "--workspace-folder", ws); err != nil {
		t.Fatalf("without --cache-key: %v", err)
	}
	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"read-configuration", "--workspace-folder", ws, "--cache-key"})
	wantExitCode(t, root.Execute(), 1)
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not the error envelope: %v\n%s", err, stdout.String())
	}
	lockPath := filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")
	if msg, _ := envelope["message"].(string); envelope["outcome"] != "error" ||
		!strings.HasPrefix(msg, "compute cache key: read features lockfile "+lockPath+": parse lockfile:") {
		t.Errorf("stdout = %s, want the error outcome naming %s", stdout.String(), lockPath)
	}
}

func TestReadConfigurationCommandFeaturesAndCacheKey(t *testing.T) {
	files := map[string]string{".devcontainer/devcontainer.json": `{"image": "img:1", "features": {"./feat": {}}}`}
	for k, v := range localFeatureFiles {
		files[k] = v
	}
	ws := writeConfigFixture(t, files)

	out, err := runReadConfigurationCmd(t, "--workspace-folder", ws, "--include-features-configuration", "--cache-key")
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := out["configuration"].(map[string]any); cfg["image"] != "img:1" {
		t.Errorf("configuration = %v", out["configuration"])
	}
	if key, _ := out["cacheKey"].(string); key == "" {
		t.Errorf("cacheKey = %v, want a key with --cache-key", out["cacheKey"])
	}
	fc, _ := out["featuresConfiguration"].(map[string]any)
	if sets, _ := fc["featureSets"].([]any); len(sets) != 1 {
		t.Errorf("featuresConfiguration = %v, want the local Feature's set", out["featuresConfiguration"])
	}
	if wsOut, _ := out["workspace"].(map[string]any); wsOut["workspaceFolder"] == nil {
		t.Errorf("workspace = %v, want the workspace folder", out["workspace"])
	}

	out, err = runReadConfigurationCmd(t, "--workspace-folder", ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cacheKey", "featuresConfiguration", "mergedConfiguration"} {
		if v, ok := out[k]; ok {
			t.Errorf("%s = %v without its flag, want it left out", k, v)
		}
	}
}

func TestReadConfigurationCommandUnresolvableFeatureExitsOne(t *testing.T) {
	ws := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image": "img:1", "features": {"./missing": {}}}`})
	_, err := runReadConfigurationCmd(t, "--workspace-folder", ws, "--include-features-configuration")
	wantExitCode(t, err, 1)
}

func TestReadConfigurationCommandMergedWithoutContainer(t *testing.T) {
	// A Dockerfile without FROM names no base image, so nothing is inspected:
	// the merged configuration is the Features' metadata and the config's.
	files := map[string]string{
		".devcontainer/devcontainer.json": `{"build": {"dockerfile": "Dockerfile"}, "remoteUser": "me", "features": {"./feat": {}}}`,
		".devcontainer/Dockerfile":        "ARG X=1\n",
	}
	for k, v := range localFeatureFiles {
		files[k] = v
	}
	ws := writeConfigFixture(t, files)
	out, err := runReadConfigurationCmd(t, "--workspace-folder", ws, "--include-merged-configuration")
	if err != nil {
		t.Fatal(err)
	}
	merged, _ := out["mergedConfiguration"].(map[string]any)
	if merged["remoteUser"] != "me" {
		t.Errorf("mergedConfiguration.remoteUser = %v, want the config's", merged["remoteUser"])
	}
	if !reflect.DeepEqual(merged["capAdd"], []any{"SYS_PTRACE"}) {
		t.Errorf("mergedConfiguration.capAdd = %v, want the Feature's", merged["capAdd"])
	}
	if _, ok := out["featuresConfiguration"]; !ok {
		t.Errorf("featuresConfiguration missing; the merged configuration without a container resolves the Features")
	}
}

func TestReadConfigurationCommandMergedWithoutImageExitsOne(t *testing.T) {
	ws := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"name": "no image"}`})
	_, err := runReadConfigurationCmd(t, "--workspace-folder", ws, "--include-merged-configuration")
	wantExitCode(t, err, 1)
	if !strings.Contains(err.Error(), "No image information specified in devcontainer.json.") {
		t.Errorf("err = %v, want the missing image named", err)
	}
}

func TestReadConfigurationCommandWithoutDockerEngine(t *testing.T) {
	// Without a Docker engine the configuration is still read, with a warning.
	t.Setenv("DOCKER_HOST", "not a docker host")
	ws := writeConfigFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image": "img:1"}`})
	logFile := filepath.Join(t.TempDir(), "log.txt")
	out, err := runReadConfigurationCmd(t, "--workspace-folder", ws, "--log-file", logFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := out["configuration"].(map[string]any); cfg["image"] != "img:1" {
		t.Errorf("configuration = %v", out["configuration"])
	}
	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "Warning: Docker engine:") {
		t.Errorf("log = %q, want the Docker engine warning", logged)
	}
}

func TestReadConfigurationImageBuildInfo(t *testing.T) {
	ctx := context.Background()
	if _, err := readConfigurationImageBuildInfo(ctx, log.Null, nil, "", nil); err == nil || err.Error() != "No image information specified in devcontainer.json." {
		t.Errorf("without a config: err = %v, want the missing image", err)
	}

	_, noFrom := loadFixtureConfig(t, `{"build": {"dockerfile": "Dockerfile"}}`, map[string]string{".devcontainer/Dockerfile": "ARG X=1\n"})
	info, err := readConfigurationImageBuildInfo(ctx, log.Null, nil, "", noFrom)
	if err != nil || info.User != "root" || len(info.Metadata) != 0 {
		t.Errorf("Dockerfile without a base image: %+v, %v; want user root and no metadata", info, err)
	}

	_, compose := loadFixtureConfig(t, `{"dockerComposeFile": "compose.yml", "service": "app"}`, map[string]string{".devcontainer/compose.yml": "services: {app: {image: img}}\n"})
	_, err = readConfigurationImageBuildInfo(ctx, log.Null, nil, filepath.Join(t.TempDir(), "no-docker"), compose)
	if err == nil || !strings.Contains(err.Error(), "'docker compose' (v2) not found") {
		t.Errorf("compose without docker compose: err = %v, want it named", err)
	}
}
