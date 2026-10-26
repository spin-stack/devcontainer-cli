package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/lifecycle"
)

func validRunUserCommandsOpts() *runUserCommandsOpts {
	return &runUserCommandsOpts{
		workspaceFolder:     "/ws",
		logLevel:            "info",
		logFormat:           "text",
		defaultUserEnvProbe: "loginInteractiveShell",
	}
}

func TestPrepareRunUserCommandsOptsRejectsInvalidFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*runUserCommandsOpts)
		error string
	}{
		{"id-label", func(o *runUserCommandsOpts) { o.idLabels = []string{"broken"} }, "id-label must match"},
		{"remote-env", func(o *runUserCommandsOpts) { o.remoteEnvs = []string{"broken"} }, "remote-env must match"},
		{"default-user-env-probe", func(o *runUserCommandsOpts) { o.defaultUserEnvProbe = "broken" }, "default-user-env-probe"},
		{"terminal-columns", func(o *runUserCommandsOpts) { o.terminalColumns = givenInt{value: 80, given: true} }, "terminal-columns -> terminal-rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := validRunUserCommandsOpts()
			tc.edit(opts)
			if err := prepareRunUserCommandsOpts(opts); err == nil || !strings.Contains(err.Error(), tc.error) {
				t.Errorf("error = %v, want one containing %q", err, tc.error)
			}
		})
	}
	if err := prepareRunUserCommandsOpts(validRunUserCommandsOpts()); err != nil {
		t.Errorf("valid flags: %v", err)
	}
}

// Without --workspace-folder, --container-id or --id-label the workspace is the
// current directory; any of them says which container to use instead.
func TestPrepareRunUserCommandsOptsDefaultsTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*runUserCommandsOpts)
		want string
	}{
		{"nothing given", func(*runUserCommandsOpts) {}, cwd},
		{"workspace folder", func(o *runUserCommandsOpts) { o.workspaceFolder = "/given" }, "/given"},
		{"container id", func(o *runUserCommandsOpts) { o.containerID = "abc" }, ""},
		{"id label", func(o *runUserCommandsOpts) { o.idLabels = []string{"a=b"} }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := validRunUserCommandsOpts()
			opts.workspaceFolder = ""
			tc.edit(opts)
			if err := prepareRunUserCommandsOpts(opts); err != nil {
				t.Fatal(err)
			}
			if opts.workspaceFolder != tc.want {
				t.Errorf("workspaceFolder = %q, want %q", opts.workspaceFolder, tc.want)
			}
		})
	}
}

func TestRunUserCommands_InvalidFlag_PreDocker(t *testing.T) {
	opts := validRunUserCommandsOpts()
	opts.remoteEnvs = []string{"broken"}
	out := &captureOutput{}
	ece := asExitCode(t, runUserCommands(t.Context(), out, opts))
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	if !strings.Contains(out.err.String(), "remote-env must match") {
		t.Errorf("stderr = %q, want the --remote-env message", out.err.String())
	}
	if out.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty on a validation error", out.out.String())
	}
}

func TestRunUserCommands_ConfigLoadError_PreDocker(t *testing.T) {
	opts := validRunUserCommandsOpts()
	opts.workspaceFolder = t.TempDir() // no devcontainer.json
	out := &captureOutput{}
	err := runUserCommands(t.Context(), out, opts)
	ece := asExitCode(t, err)
	if ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	if !strings.Contains(out.out.String(), `"outcome":"error"`) || !strings.Contains(out.out.String(), "Dev container config") {
		t.Errorf("stdout = %q, want the config-not-found error envelope", out.out.String())
	}
	if strings.Contains(out.out.String(), "Dev container not found") || strings.Contains(err.Error(), "Docker engine") {
		t.Errorf("went on to look for the container: %q", out.out.String())
	}
}

// A container lookup that fails is TS's error: the failed `docker ps` by the
// workspace folder's and the configuration's labels as the message, running
// the user commands as the description. Go reported "Dev container not
// found.".
func TestRunUserCommandsFailedContainerLookup(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/devcontainer-cli-test.sock")
	opts := validRunUserCommandsOpts()
	opts.workspaceFolder = t.TempDir()
	cfg := filepath.Join(opts.workspaceFolder, ".devcontainer", "devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{"image":"img"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &captureOutput{}
	err := runUserCommands(t.Context(), out, opts)
	if ece := asExitCode(t, err); ece.Code != 1 {
		t.Errorf("exit code = %d, want 1", ece.Code)
	}
	want := `{"outcome":"error","message":"Command failed: docker ps -q -a --filter label=devcontainer.local_folder=` + opts.workspaceFolder + ` --filter label=devcontainer.config_file=` + cfg + `","description":"An error occurred running user commands in the container."}`
	if got := strings.TrimSpace(out.out.String()); got != want {
		t.Errorf("stdout = %s\nwant     %s", got, want)
	}
}

func TestConfigFromFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(`{"image":"alpine","remoteUser":"dev"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if cfg := configFromFlag("", ""); cfg != nil {
		t.Errorf("no --config: %+v, want nil", cfg)
	}
	for _, path := range []string{filepath.Join(dir, "devcontainer.json"), "devcontainer.json"} {
		if cfg := configFromFlag(path, ""); cfg == nil || cfg.RemoteUser != "dev" {
			t.Errorf("--config %s: %+v, want the config loaded", path, cfg)
		}
	}
	if cfg := configFromFlag("missing.json", ""); cfg != nil {
		t.Errorf("unloadable --config: %+v, want nil (the container metadata is used)", cfg)
	}
}

func TestLifecycleRemoteUser(t *testing.T) {
	merged := &imagemeta.MergedConfig{RemoteUser: "image-user"}
	for _, tc := range []struct {
		name string
		cfg  *config.DevContainer
		want string
	}{
		{"no config", nil, "image-user"},
		{"config without remoteUser", &config.DevContainer{}, "image-user"},
		{"config remoteUser wins", &config.DevContainer{RemoteUser: "dev"}, "dev"},
	} {
		if got := lifecycleRemoteUser(merged, tc.cfg); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLifecycleUserEnvProbe(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.DevContainer
		want lifecycle.UserEnvProbeStrategy
	}{
		{"no config", nil, "loginShell"},
		{"config without userEnvProbe", &config.DevContainer{}, "loginShell"},
		{"config userEnvProbe wins", &config.DevContainer{UserEnvProbe: "none"}, "none"},
	} {
		if got := lifecycleUserEnvProbe("loginShell", tc.cfg); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Later entries win, so the order is the precedence: probe, secrets,
// --remote-env, then the config's remoteEnv.
func TestLifecycleRemoteEnvOrder(t *testing.T) {
	probed := map[string]string{"PATH": "/probed"}
	secrets := []string{"TOKEN=s"}
	cli := []string{"PATH=/cli"}
	value := "/config"
	cfg := &config.DevContainer{RemoteEnv: map[string]*string{"PATH": &value, "UNSET": nil}}

	if got, want := lifecycleRemoteEnv(probed, secrets, cli, cfg), []string{"PATH=/probed", "TOKEN=s", "PATH=/cli", "PATH=/config"}; !slices.Equal(got, want) {
		t.Errorf("with config: %v, want %v", got, want)
	}
	if got, want := lifecycleRemoteEnv(probed, secrets, cli, nil), []string{"PATH=/probed", "TOKEN=s", "PATH=/cli"}; !slices.Equal(got, want) {
		t.Errorf("without config: %v, want %v", got, want)
	}
}
