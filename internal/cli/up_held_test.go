package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	dockermount "github.com/moby/moby/api/types/mount"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
)

func quietLogger() log.Logger { return log.New(log.Options{Writer: io.Discard}) }

func TestResolveUpPathsDefaultsTheWorkspaceToTheCurrentDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, tc := range []struct {
		name string
		opts upOpts
		want [3]string
	}{
		{"nothing given", upOpts{}, [3]string{cwd, "", ""}},
		{"id label", upOpts{idLabels: []string{"a=b"}}, [3]string{"", "", ""}},
		{"override config", upOpts{overrideConfig: "o.json"}, [3]string{"", "", filepath.Join(cwd, "o.json")}},
		{"workspace", upOpts{workspaceFolder: "ws", configPath: "/c.json"}, [3]string{filepath.Join(cwd, "ws"), "/c.json", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, cfg, ov := resolveUpPaths(&tc.opts)
			if got := [3]string{ws, cfg, ov}; got != tc.want {
				t.Errorf("resolveUpPaths = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpHasNothingToReadOnlyWithoutWorkspaceAndConfig(t *testing.T) {
	for _, tc := range []struct {
		ws, cfg, ov string
		want        bool
	}{
		{"", "", "", true},
		{"/ws", "", "", false},
		{"", "/c.json", "", false},
		{"", "", "/o.json", false},
	} {
		if got := upHasNothingToRead(tc.ws, tc.cfg, tc.ov); got != tc.want {
			t.Errorf("upHasNothingToRead(%q, %q, %q) = %v, want %v", tc.ws, tc.cfg, tc.ov, got, tc.want)
		}
	}
}

func metadataLabels(json string) map[string]string {
	return map[string]string{imagemeta.MetadataLabel: json}
}

func TestUpRemoteUserFallsBackInTSOrder(t *testing.T) {
	withUser := func(user string, labels map[string]string) container.InspectResponse {
		return container.InspectResponse{Image: "sha256:img", Config: &container.Config{User: user, Labels: labels}}
	}
	for _, tc := range []struct {
		name        string
		cfgUser     string
		ctr         container.InspectResponse
		imageLabels map[string]string
		want        string
		wantImage   bool // whether the image had to be asked
	}{
		{"config", "cfg", withUser("ctr", metadataLabels(`[{"remoteUser":"meta"}]`)), metadataLabels(`[{"remoteUser":"img"}]`), "cfg", false},
		{"container metadata", "", withUser("ctr", metadataLabels(`[{"remoteUser":"meta"}]`)), metadataLabels(`[{"remoteUser":"img"}]`), "meta", false},
		{"image metadata", "", withUser("ctr", metadataLabels(`[{"containerEnv":{"A":"b"}}]`)), metadataLabels(`[{"remoteUser":"img"}]`), "img", true},
		{"container USER", "", withUser("ctr", nil), nil, "ctr", true},
		{"root", "", withUser("", nil), nil, "root", true},
		{"no container config", "", container.InspectResponse{Image: "sha256:img"}, metadataLabels(`[{"remoteUser":"img"}]`), "img", true},
		{"nothing at all", "", container.InspectResponse{}, nil, "root", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			imageLabels := func(ref string) map[string]string {
				asked = append(asked, ref)
				return tc.imageLabels
			}
			if got := upRemoteUser(tc.cfgUser, tc.ctr, imageLabels, quietLogger()); got != tc.want {
				t.Errorf("upRemoteUser = %q, want %q", got, tc.want)
			}
			if tc.wantImage != (len(asked) > 0) {
				t.Errorf("image asked %v, want asked = %v", asked, tc.wantImage)
			}
			if len(asked) > 0 && asked[0] != tc.ctr.Image {
				t.Errorf("image asked for %q, want the container's image %q", asked[0], tc.ctr.Image)
			}
		})
	}
}

// containerMetadata reads a container's metadata labels without substitution
// or id labels: its entries, then the configuration's.
func containerMetadata(labels map[string]string) func(container.InspectResponse, *config.DevContainer) ([]imagemeta.Entry, error) {
	return func(_ container.InspectResponse, cfg *config.DevContainer) ([]imagemeta.Entry, error) {
		return imageMetadataFromContainer(labels, cfg, nil, nil, func(*[]imagemeta.Entry) error { return nil }, quietLogger())
	}
}

// upConfigurationJSON is what addUpConfiguration adds to up's result, as up
// prints it, for a container whose metadata labels are containerLabels.
func upConfigurationJSON(t *testing.T, opts *upOpts, cfg *config.DevContainer, ctr container.InspectResponse, containerLabels map[string]string) map[string]any {
	t.Helper()
	result := map[string]any{}
	if err := addUpConfiguration(result, opts, cfg, ctr, containerMetadata(containerLabels), quietLogger()); err != nil {
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

func TestAddUpConfigurationNamesTheConfigFileAsTSDoes(t *testing.T) {
	const cfp = "/ws/.devcontainer/devcontainer.json"
	for _, tc := range []struct {
		name   string
		opts   upOpts
		scheme string
	}{
		{"found in the workspace", upOpts{}, "vscode-fileHost"},
		{"--config", upOpts{configPath: "c.json"}, "file"},
		{"--override-config", upOpts{overrideConfig: "o.json"}, "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.includeConfig, tc.opts.includeMergedConfig = true, true
			got := upConfigurationJSON(t, &tc.opts, &config.DevContainer{ConfigFilePath: cfp, Image: "alpine:3"}, container.InspectResponse{}, nil)

			want := map[string]any{"$mid": float64(1), "path": cfp, "scheme": tc.scheme}
			conf, _ := got["configuration"].(map[string]any)
			if !reflect.DeepEqual(conf["configFilePath"], want) {
				t.Errorf("configuration.configFilePath = %v, want %v", conf["configFilePath"], want)
			}
			mc, _ := got["mergedConfiguration"].(map[string]any)
			if !reflect.DeepEqual(mc["configFilePath"], want) {
				t.Errorf("mergedConfiguration.configFilePath = %v, want %v", mc["configFilePath"], want)
			}
		})
	}
}

func TestAddUpConfigurationResolvesTheContainerEnv(t *testing.T) {
	cfg := &config.DevContainer{
		Image:     "alpine:3",
		RemoteEnv: map[string]*string{"FROM_CONTAINER": strPtr("${containerEnv:FOO}")},
	}
	ctr := container.InspectResponse{Image: "sha256:img", Config: &container.Config{Env: []string{"FOO=bar"}}}
	imageLabels := metadataLabels(`[{"remoteUser":"node","postCreateCommand":"from-image"}]`)

	got := upConfigurationJSON(t, &upOpts{includeConfig: true, includeMergedConfig: true}, cfg, ctr, imageLabels)

	conf, _ := got["configuration"].(map[string]any)
	if re, _ := conf["remoteEnv"].(map[string]any); re["FROM_CONTAINER"] != "bar" {
		t.Errorf("configuration.remoteEnv = %v, want ${containerEnv:FOO} resolved to bar", conf["remoteEnv"])
	}
	for k, v := range conf {
		if v == nil {
			t.Errorf("configuration.%s is null; TS leaves unset properties out", k)
		}
	}
	if _, ok := conf["configFilePath"]; ok {
		t.Errorf("configuration.configFilePath = %v for a config with no file", conf["configFilePath"])
	}
	mc, _ := got["mergedConfiguration"].(map[string]any)
	if re, _ := mc["remoteEnv"].(map[string]any); re["FROM_CONTAINER"] != "bar" {
		t.Errorf("mergedConfiguration.remoteEnv = %v, want ${containerEnv:FOO} resolved to bar", mc["remoteEnv"])
	}
	if mc["remoteUser"] != "node" || !reflect.DeepEqual(mc["postCreateCommands"], []any{"from-image"}) {
		t.Errorf("mergedConfiguration = %v, want the image's metadata merged in", mc)
	}
	if mc["image"] != "alpine:3" {
		t.Errorf("mergedConfiguration.image = %v, want the config's image", mc["image"])
	}
}

func TestAddUpConfigurationAddsOnlyWhatWasAskedFor(t *testing.T) {
	cfg := &config.DevContainer{Image: "alpine:3"}
	if got := upConfigurationJSON(t, &upOpts{}, cfg, container.InspectResponse{}, nil); len(got) != 0 {
		t.Errorf("result = %v, want nothing added", got)
	}
	got := upConfigurationJSON(t, &upOpts{includeMergedConfig: true}, cfg, container.InspectResponse{}, nil)
	if _, ok := got["configuration"]; ok {
		t.Errorf("configuration added without --include-configuration: %v", got)
	}
	if mc, _ := got["mergedConfiguration"].(map[string]any); mc["image"] != "alpine:3" {
		t.Errorf("mergedConfiguration = %v, want it added", got["mergedConfiguration"])
	}
}

func TestAddUpConfigurationReportsAContainerEnvReferenceWithoutAName(t *testing.T) {
	cfg := &config.DevContainer{RemoteEnv: map[string]*string{"X": strPtr("${containerEnv}")}}
	labels := containerMetadata(nil)
	err := addUpConfiguration(map[string]any{}, &upOpts{includeConfig: true}, cfg, container.InspectResponse{}, labels, quietLogger())
	if err == nil || !strings.HasPrefix(err.Error(), "resolve configuration container variables: ") {
		t.Errorf("configuration error = %v, want the substitution error", err)
	}
	err = addUpConfiguration(map[string]any{}, &upOpts{includeMergedConfig: true}, cfg, container.InspectResponse{}, labels, quietLogger())
	if err == nil || !strings.HasPrefix(err.Error(), "resolve merged container variables: ") {
		t.Errorf("merged error = %v, want the substitution error", err)
	}
}

func TestFindUserArgIsTheLastUserFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-u", "a"}, "a"},
		{[]string{"--user", "b"}, "b"},
		{[]string{"-u=c"}, "c"},
		{[]string{"--user=d"}, "d"},
		{[]string{"-u", "a", "--privileged", "--user", "b"}, "b"},
		{[]string{"--user=d", "-u", "a"}, "a"},
		{[]string{"--cap-add", "SYS_PTRACE", "-u"}, ""},
		{[]string{"-u", "a", "-u"}, "a"},
		{[]string{"--name", "x"}, ""},
	} {
		if got := findUserArg(tc.args); got != tc.want {
			t.Errorf("findUserArg(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestContainerRunUserPrefersTheMetadataUser(t *testing.T) {
	img := image.InspectResponse{Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: "imageuser"}}}
	for _, tc := range []struct {
		meta string
		img  image.InspectResponse
		want string
	}{
		{"meta", img, "meta"},
		{"", img, "imageuser"},
		{"", image.InspectResponse{}, ""},
	} {
		if got := containerRunUser(tc.meta, tc.img); got != tc.want {
			t.Errorf("containerRunUser(%q, %+v) = %q, want %q", tc.meta, tc.img.Config, got, tc.want)
		}
	}
}

func TestUpWorkspaceMounts(t *testing.T) {
	bind := func(src, dst string, c dockermount.Consistency) dockermount.Mount {
		return dockermount.Mount{Type: dockermount.TypeBind, Source: src, Target: dst, Consistency: c}
	}
	for _, tc := range []struct {
		name        string
		wc          config.WorkspaceConfig
		ws          string
		consistency string
		want        []dockermount.Mount
	}{
		{"workspace bind", config.WorkspaceConfig{WorkspaceFolder: "/workspaces/p"}, "/src/p", "cached",
			[]dockermount.Mount{bind("/src/p", "/workspaces/p", "cached")}},
		{"custom workspaceMount", config.WorkspaceConfig{WorkspaceFolder: "/w", WorkspaceMount: "type=bind,source=/elsewhere,target=/w"}, "/src/p", "delegated",
			[]dockermount.Mount{bind("/elsewhere", "/w", "delegated")}},
		{"workspaceMount without a workspace", config.WorkspaceConfig{WorkspaceFolder: "/w", WorkspaceMount: "type=bind,source=/elsewhere,target=/w"}, "", "cached",
			[]dockermount.Mount{bind("/elsewhere", "/w", "cached")}},
		{"no workspace, no workspaceMount", config.WorkspaceConfig{WorkspaceFolder: "/w"}, "", "cached", nil},
		{"no consistency keeps the spec's", config.WorkspaceConfig{WorkspaceFolder: "/w", WorkspaceMount: "type=bind,source=/s,target=/w,consistency=consistent", AdditionalMounts: []string{"type=bind,source=/git,target=/git,consistency=delegated"}}, "/src", "",
			[]dockermount.Mount{bind("/s", "/w", "consistent"), bind("/git", "/git", "delegated")}},
		{"worktree common dir", config.WorkspaceConfig{WorkspaceFolder: "/w", AdditionalMounts: []string{"type=bind,source=/git,target=/git"}}, "/src", "cached",
			[]dockermount.Mount{bind("/src", "/w", "cached"), bind("/git", "/git", "cached")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := upWorkspaceMounts(&tc.wc, tc.ws, tc.consistency)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mounts = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUpWorkspaceMountsRejectsAnUnparsableSpec(t *testing.T) {
	if _, err := upWorkspaceMounts(&config.WorkspaceConfig{WorkspaceMount: "source=/s"}, "/src", "cached"); err == nil || !strings.Contains(err.Error(), "invalid workspaceMount") {
		t.Errorf("workspaceMount error = %v", err)
	}
	if _, err := upWorkspaceMounts(&config.WorkspaceConfig{AdditionalMounts: []string{"source=/s"}}, "/src", "cached"); err == nil || !strings.Contains(err.Error(), "invalid additional mount") {
		t.Errorf("additional mount error = %v", err)
	}
}

func TestParseFlagMounts(t *testing.T) {
	got, err := parseFlagMounts([]string{"type=volume,source=v,target=/v", "type=bind,source=/s,target=/t"})
	if err != nil {
		t.Fatal(err)
	}
	want := []dockermount.Mount{
		{Type: dockermount.TypeVolume, Source: "v", Target: "/v"},
		{Type: dockermount.TypeBind, Source: "/s", Target: "/t"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mounts = %+v, want %+v", got, want)
	}
	if _, err := parseFlagMounts([]string{"source=/s"}); err == nil || !strings.Contains(err.Error(), "invalid --mount") {
		t.Errorf("error = %v, want an invalid --mount error", err)
	}
}

// composeConfigRunner answers `docker compose config` with config (or fails
// when config is nil).
type composeConfigRunner struct{ config map[string]any }

func (r composeConfigRunner) Run(_ context.Context, _ io.Writer, _ string, args ...string) ([]byte, []byte, int, error) {
	if !slices.Contains(args, "config") {
		return nil, []byte("unexpected compose call"), 1, nil
	}
	if r.config == nil {
		return nil, []byte("no configuration file provided"), 1, nil
	}
	b, err := json.Marshal(r.config)
	return b, nil, 0, err
}

func TestResolveComposeProjectNameFollowsTheTSChain(t *testing.T) {
	write := func(t *testing.T, path, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	type setup struct {
		configDir    string
		composeFiles []string
	}
	// inDevcontainer lays a config and its compose file out under
	// <root>/My_Proj/.devcontainer.
	inDevcontainer := func(t *testing.T, compose string) setup {
		dir := filepath.Join(t.TempDir(), "My_Proj", ".devcontainer")
		return setup{dir, []string{write(t, filepath.Join(dir, "docker-compose.yml"), compose)}}
	}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		dotEnv  string
		compose string
		config  map[string]any
		layout  func(t *testing.T, compose string) setup
		want    string
	}{
		{"environment", map[string]string{"COMPOSE_PROJECT_NAME": "From.Env"}, "COMPOSE_PROJECT_NAME=dotenv", "", map[string]any{"name": "named"}, inDevcontainer, "fromenv"},
		{"empty environment", map[string]string{"COMPOSE_PROJECT_NAME": ""}, "", "", nil, inDevcontainer, "my_proj_devcontainer"},
		{".env", nil, "OTHER=1\n  COMPOSE_PROJECT_NAME= Dot_Env \n", "", map[string]any{"name": "named"}, inDevcontainer, "dot_env"},
		{"empty .env", nil, "COMPOSE_PROJECT_NAME=\n", "", nil, inDevcontainer, "my_proj_devcontainer"},
		{"compose name", nil, "", "", map[string]any{"name": "Named"}, inDevcontainer, "named"},
		{"empty compose name", nil, "", "", map[string]any{"name": ""}, inDevcontainer, "my_proj_devcontainer"},
		{"compose name devcontainer, written", nil, "", "name: devcontainer\n", map[string]any{"name": "devcontainer"}, inDevcontainer, "devcontainer"},
		{"compose name devcontainer, defaulted", nil, "", "services: {}\n", map[string]any{"name": "devcontainer"}, inDevcontainer, "my_proj_devcontainer"},
		{"compose config fails", nil, "", "", nil, inDevcontainer, "my_proj_devcontainer"},
		{"outside .devcontainer", nil, "", "", nil, func(t *testing.T, compose string) setup {
			root := t.TempDir()
			return setup{filepath.Join(root, "cfg"), []string{
				write(t, filepath.Join(root, "First_Dir", "a.yml"), compose),
				write(t, filepath.Join(root, "second", "b.yml"), compose),
			}}
		}, "first_dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.layout(t, tc.compose)
			if tc.dotEnv != "" {
				write(t, filepath.Join(s.configDir, ".env"), tc.dotEnv)
			}
			cfg := &config.DevContainer{ConfigFilePath: filepath.Join(s.configDir, "devcontainer.json")}
			client := &docker.ComposeClient{Command: []string{"docker", "compose"}, Version: []int{2, 24, 0}, Log: quietLogger(), Runner: composeConfigRunner{tc.config}}

			if got := resolveComposeProjectName(context.Background(), cfg, tc.env, s.composeFiles, client); got != tc.want {
				t.Errorf("project name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseComposeServiceBuildReadsTheServicesBuild(t *testing.T) {
	cfg := map[string]any{"services": map[string]any{
		"app": map[string]any{
			"image": "app:dev",
			"build": map[string]any{"dockerfile": "Dockerfile.dev", "context": "/ctx", "target": "dev", "args": map[string]any{"N": 1.0, "S": "s"}},
		},
		"db": map[string]any{"image": "postgres"},
	}}
	want := composeServiceBuild{Image: "app:dev", HasBuild: true, Dockerfile: "Dockerfile.dev", Context: "/ctx", Target: "dev", Args: map[string]string{"N": "1", "S": "s"}}
	if got := parseComposeServiceBuild(cfg, "app"); !reflect.DeepEqual(got, want) {
		t.Errorf("app = %+v, want %+v", got, want)
	}
	if got := parseComposeServiceBuild(cfg, "db"); !reflect.DeepEqual(got, composeServiceBuild{Image: "postgres"}) {
		t.Errorf("db = %+v, want its image and no build", got)
	}
}

func TestSecretsRemoteEnv(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(good, []byte(`{"TOKEN":"s3cr3t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       []string
		warns      bool
	}{
		{"no file", "", nil, false},
		{"file", good, []string{"TOKEN=s3cr3t"}, false},
		{"missing file", filepath.Join(dir, "missing.json"), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logged bytes.Buffer
			got := secretsRemoteEnv(tc.path, log.New(log.Options{Writer: &logged}))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("env = %q, want %q", got, tc.want)
			}
			if warned := strings.Contains(logged.String(), "Warning: secrets file:"); warned != tc.warns {
				t.Errorf("log = %q, want a warning: %v", logged.String(), tc.warns)
			}
		})
	}
}

func TestHookRemoteEnvOrdersLaterSourcesLast(t *testing.T) {
	got := hookRemoteEnv(
		map[string]string{"P": "probed"},
		[]string{"S=secret", "R=cli"},
		map[string]*string{"C": strPtr("config"), "UNSET": nil},
	)
	if want := []string{"P=probed", "S=secret", "R=cli", "C=config"}; !reflect.DeepEqual(got, want) {
		t.Errorf("env = %q, want %q", got, want)
	}
}

func TestDropRanHooksAsksEveryMarkerAndDropsWhatRan(t *testing.T) {
	inspect := container.InspectResponse{Created: "created", State: &container.State{StartedAt: "started"}}
	allHooks := func() *imagemeta.MergedConfig {
		return &imagemeta.MergedConfig{
			OnCreateCommands: []any{"oc"}, UpdateContentCommands: []any{"uc"}, PostCreateCommands: []any{"pc"},
			PostStartCommands: []any{"ps"}, PostAttachCommands: []any{"pa"},
		}
	}
	wantAsked := []string{"onCreateCommand@created", "updateContentCommand@created", "postCreateCommand@created", "postStartCommand@started"}

	for _, tc := range []struct {
		name     string
		ran      map[string]bool
		prebuild bool
		want     *imagemeta.MergedConfig
	}{
		{"first run", nil, false, allHooks()},
		{"all ran", map[string]bool{"onCreateCommand": true, "updateContentCommand": true, "postCreateCommand": true, "postStartCommand": true}, false,
			&imagemeta.MergedConfig{PostAttachCommands: []any{"pa"}}},
		{"restarted", map[string]bool{"onCreateCommand": true, "updateContentCommand": true, "postCreateCommand": true}, false,
			&imagemeta.MergedConfig{PostStartCommands: []any{"ps"}, PostAttachCommands: []any{"pa"}}},
		{"prebuild reruns updateContent", map[string]bool{"onCreateCommand": true, "updateContentCommand": true, "postCreateCommand": true, "postStartCommand": true}, true,
			&imagemeta.MergedConfig{UpdateContentCommands: []any{"uc"}, PostAttachCommands: []any{"pa"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			merged := allHooks()
			dropRanHooks(merged, inspect, tc.prebuild, func(hook, timestamp string) bool {
				asked = append(asked, hook+"@"+timestamp)
				return !tc.ran[hook]
			})
			if !reflect.DeepEqual(merged, tc.want) {
				t.Errorf("hooks = %+v, want %+v", merged, tc.want)
			}
			if !slices.Equal(asked, wantAsked) {
				t.Errorf("markers asked %v, want %v", asked, wantAsked)
			}
		})
	}
}

func TestDropRanHooksWithoutAStartTimeAsksWithNone(t *testing.T) {
	var asked []string
	dropRanHooks(&imagemeta.MergedConfig{}, container.InspectResponse{Created: "created"}, false, func(hook, timestamp string) bool {
		asked = append(asked, hook+"@"+timestamp)
		return true
	})
	if asked[len(asked)-1] != "postStartCommand@" {
		t.Errorf("markers asked %v, want postStart asked with no timestamp", asked)
	}
}

// recordingExecutor records the commands it is asked to run and fails them
// with err.
type recordingExecutor struct {
	commands []string
	err      error
}

func (e *recordingExecutor) Exec(command string) error {
	e.commands = append(e.commands, command)
	return e.err
}

var _ lifecycle.CommandExecutor = (*recordingExecutor)(nil)

func TestDotfilesAfterPostCreateInstallsAndOnlyWarnsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		repo     string
		err      error
		installs bool
		warns    bool
	}{
		{"no repository", "", nil, false, false},
		{"installed", "me/dotfiles", nil, true, false},
		{"failed", "me/dotfiles", errors.New("exit status 128"), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logged bytes.Buffer
			exec := &recordingExecutor{err: tc.err}
			dotfilesAfterPostCreate(log.New(log.Options{Writer: &logged}), exec, lifecycle.DotfilesConfig{Repository: tc.repo})()
			if installs := len(exec.commands) > 0; installs != tc.installs {
				t.Errorf("ran %d commands, want an install: %v", len(exec.commands), tc.installs)
			}
			warned := strings.Contains(logged.String(), "Dotfiles installation failed: exit status 128")
			if warned != tc.warns {
				t.Errorf("log = %q, want the failure warned: %v", logged.String(), tc.warns)
			}
		})
	}
}

// TestE2E_UpCacheImage starts a container from a prebuilt --cache-image instead
// of the config's image, and reports a cache image it cannot pull.
func TestE2E_UpCacheImage(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	tag := fmt.Sprintf("devcontainer-cli-e2e-cache-image:%d", time.Now().UnixNano())
	build := osexec.Command("docker", "build", "-q", "-t", tag, "-")
	build.Stdin = strings.NewReader("FROM alpine:3\nRUN touch /prebuilt-marker\n")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	defer osexec.Command("docker", "rmi", "-f", tag).Run()

	ws := writeFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image":"alpine:3"}`})
	upOut := runCLI(t, "up", "--workspace-folder", ws, "--cache-image", tag, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	id, _ := parseJSON(t, upOut)["containerId"].(string)
	if id == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(id)
	if got := dockerExec(t, id, "test -f /prebuilt-marker && echo prebuilt"); got != "prebuilt" {
		t.Errorf("container was not started from the cache image: %q", got)
	}

	missing := writeFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image":"alpine:3"}`})
	failOut := runCLI(t, "up", "--workspace-folder", missing, "--cache-image", "127.0.0.1:1/missing/image:none", "--skip-post-create")
	result := parseJSON(t, failOut)
	if id, _ := result["containerId"].(string); id != "" {
		removeByID(id)
	}
	if desc, _ := result["description"].(string); result["outcome"] != "error" || !strings.Contains(desc, "pull cache image") {
		t.Errorf("up with a cache image it cannot pull = %v, want the pull error", result)
	}
}

// TestE2E_UpReusesStoppedContainer starts the workspace's stopped container
// again instead of creating another one.
func TestE2E_UpReusesStoppedContainer(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ws := writeFixture(t, map[string]string{".devcontainer/devcontainer.json": `{"image":"alpine:3"}`})

	first := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, first, "success")
	id, _ := parseJSON(t, first)["containerId"].(string)
	if id == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(id)
	if out, err := osexec.Command("docker", "stop", "-t", "0", id).CombinedOutput(); err != nil {
		t.Fatalf("docker stop: %v\n%s", err, out)
	}

	second := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, second, "success")
	if again, _ := parseJSON(t, second)["containerId"].(string); again != id {
		removeByID(again)
		t.Fatalf("second up used container %q, want the stopped %q", again, id)
	}
	running, err := osexec.Command("docker", "inspect", "--format", "{{.State.Running}}", id).Output()
	if err != nil || strings.TrimSpace(string(running)) != "true" {
		t.Errorf("reused container running = %q (%v), want true", running, err)
	}
}

func TestAddUpConfigurationDropsAConfigFilePathThatResolvesToNothing(t *testing.T) {
	cfg := &config.DevContainer{ConfigFilePath: "${containerEnv:UNSET}"}

	got := upConfigurationJSON(t, &upOpts{includeConfig: true, includeMergedConfig: true, configPath: "c.json"}, cfg, container.InspectResponse{}, nil)

	conf, _ := got["configuration"].(map[string]any)
	if v, ok := conf["configFilePath"]; ok {
		t.Errorf("configuration.configFilePath = %v, want it left out", v)
	}
	mc, _ := got["mergedConfiguration"].(map[string]any)
	if v, ok := mc["configFilePath"]; ok {
		t.Errorf("mergedConfiguration.configFilePath = %v, want it left out", v)
	}
}

// up's Features build writes the configuration's containerEnv into the image
// as ENV lines, as TS getFeaturesBuildOptions does and build already did.
func TestUpFeatureBuildOptionsCarryTheContainerEnv(t *testing.T) {
	cfg := &config.DevContainer{ContainerEnv: map[string]string{"VAR_WITH_SPACES": "value with spaces"}}
	got := (&upRunner{opts: &upOpts{}}).featureBuildOptions(cfg, &config.LoadResult{}, "").ContainerEnv
	if !reflect.DeepEqual(got, cfg.ContainerEnv) {
		t.Errorf("ContainerEnv = %v, want the config's %v", got, cfg.ContainerEnv)
	}
}
