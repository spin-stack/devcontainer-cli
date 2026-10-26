package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/lifecycle"
	"github.com/devcontainers/cli/internal/log"
	"github.com/moby/moby/api/types/container"
)

func runExecCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newExecCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// Help before the command prints the usage; no command is an error; a command
// reaches exec, whose flag validation runs first.
func TestExecCommandArguments(t *testing.T) {
	for _, h := range []string{"-h", "--help"} {
		out, err := runExecCommand(t, h)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Errorf("%s: err = %v, output = %q", h, err, out)
		}
	}
	if _, err := runExecCommand(t, "--workspace-folder", t.TempDir()); err == nil || err.Error() != "exec requires at least 1 arg" {
		t.Errorf("no command: err = %v", err)
	}
	if _, err := runExecCommand(t, "--log-level", "loud", "echo"); err == nil || !strings.Contains(err.Error(), "log-level") {
		t.Errorf("one-word command: err = %v, want the log-level validation", err)
	}
}

func TestExecDefaultsToCwd(t *testing.T) {
	cases := []struct {
		opts execOpts
		want bool
	}{
		{execOpts{}, true},
		{execOpts{workspaceFolder: "ws"}, false},
		{execOpts{idLabels: []string{"a=b"}}, false},
		{execOpts{containerID: "c"}, false},
	}
	for _, c := range cases {
		if got := execDefaultsToCwd(&c.opts); got != c.want {
			t.Errorf("execDefaultsToCwd(%+v) = %v, want %v", c.opts, got, c.want)
		}
	}
}

func TestExecConfigPath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "c.json")
	for in, want := range map[string]string{"": "", "rel/c.json": "/work/rel/c.json", abs: abs} {
		if got := execConfigPath(in, "/work"); got != want {
			t.Errorf("execConfigPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TS exec reads the configuration the flags name, with or without
// --container-id, and fails when it cannot: --config without
// --workspace-folder is read, and a missing one is not found.
func TestExecLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"img","remoteUser":"u"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(dir, "o.json")
	if err := os.WriteFile(override, []byte(`{"image":"img","remoteUser":"o"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := execLoadConfig(&execOpts{containerID: "c"}, dir); got != nil || err != nil {
		t.Errorf("nothing named = %v, %v; want no configuration", got, err)
	}
	if got, err := execLoadConfig(&execOpts{containerID: "c", configPath: "c.json"}, dir); err != nil || got.Config.RemoteUser != "u" || got.Config.ConfigFilePath != cfg {
		t.Errorf("--config with --container-id = %+v, %v; want c.json read", got, err)
	}
	if got, err := execLoadConfig(&execOpts{containerID: "c", overrideConfig: "o.json"}, dir); err != nil || got.Config.RemoteUser != "o" {
		t.Errorf("--override-config = %+v, %v; want o.json read", got, err)
	}
	missing := filepath.Join(dir, "missing.json")
	want := "Dev container config (" + missing + ") not found."
	if _, err := execLoadConfig(&execOpts{containerID: "c", configPath: "missing.json"}, dir); err == nil || err.Error() != want {
		t.Errorf("missing --config: err = %v, want %q", err, want)
	}
	ws := t.TempDir()
	want = "Dev container config (" + filepath.Join(ws, ".devcontainer", "devcontainer.json") + ") not found."
	if _, err := execLoadConfig(&execOpts{containerID: "c", workspaceFolder: ws}, dir); err == nil || err.Error() != want {
		t.Errorf("workspace without a configuration: err = %v, want %q", err, want)
	}
}

func TestContainerRunning(t *testing.T) {
	if containerRunning(nil) || containerRunning(&container.State{}) || !containerRunning(&container.State{Running: true}) {
		t.Error("containerRunning must hold only for a running state")
	}
}

func TestExecRemoteUserAndProbe(t *testing.T) {
	if got := execRemoteUser("node", nil); got != "node" {
		t.Errorf("no config: %q", got)
	}
	if got := execRemoteUser("node", &config.DevContainer{}); got != "node" {
		t.Errorf("config without remoteUser: %q", got)
	}
	if got := execRemoteUser("node", &config.DevContainer{RemoteUser: "vscode"}); got != "vscode" {
		t.Errorf("config remoteUser: %q", got)
	}
	if got := execProbeStrategy("loginShell", nil); got != lifecycle.UserEnvProbeStrategy("loginShell") {
		t.Errorf("no config: %q", got)
	}
	if got := execProbeStrategy("loginShell", &config.DevContainer{}); got != lifecycle.UserEnvProbeStrategy("loginShell") {
		t.Errorf("config without userEnvProbe: %q", got)
	}
	if got := execProbeStrategy("loginShell", &config.DevContainer{UserEnvProbe: "none"}); got != lifecycle.UserEnvProbeStrategy("none") {
		t.Errorf("config userEnvProbe: %q", got)
	}
}

func TestExecRemoteEnv(t *testing.T) {
	str := func(s string) *string { return &s }
	probed := map[string]string{"A": "probed", "B": "probed", "C": "probed"}
	metadata := map[string]*string{"B": str("meta"), "C": nil, "D": str("${containerEnv:HOME}/bin")}
	cfg := &config.DevContainer{RemoteEnv: map[string]*string{"C": str("config"), "B": nil}}
	got, err := execRemoteEnv(probed, metadata, cfg, map[string]string{"HOME": "/root"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "probed", "B": "meta", "C": "config", "D": "/root/bin"}
	if len(got) != len(want) {
		t.Errorf("env = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	if got, err := execRemoteEnv(nil, metadata, nil, nil); err != nil || got["B"] != "meta" || len(got) != 2 {
		t.Errorf("without a config: env = %v, err = %v", got, err)
	}
	if _, err := execRemoteEnv(nil, map[string]*string{"X": str("${containerEnv}")}, nil, nil); err == nil {
		t.Error("an unresolvable ${containerEnv}: want an error")
	}
	// TS's ContainerError text, naming the configuration's file.
	withFile := &config.DevContainer{ConfigFilePath: "/ws/c.json", RemoteEnv: map[string]*string{"X": str("${containerEnv}")}}
	wantErr := "'${containerEnv}' in c.json can not be resolved because no environment variable name is given."
	if _, err := execRemoteEnv(nil, nil, withFile, nil); err == nil || err.Error() != wantErr {
		t.Errorf("unresolvable in the configuration: err = %v, want %q", err, wantErr)
	}
}

// TS runs containerSubstitute over the whole configuration and merged
// configuration: an unnamed ${containerEnv} anywhere is its ContainerError,
// named ones resolve.
func TestCheckContainerSubstitution(t *testing.T) {
	env := map[string]string{"P": "/bin"}
	ok := map[string]any{"postCreateCommand": "echo ${containerEnv:P}"}
	bad := map[string]any{"postCreateCommand": []any{"echo", "${containerEnv}"}}
	if err := checkContainerSubstitution(env, "/ws/c.json", ok, nil); err != nil {
		t.Errorf("resolvable: err = %v", err)
	}
	want := "'${containerEnv}' in c.json can not be resolved because no environment variable name is given."
	err := checkContainerSubstitution(env, "/ws/c.json", ok, bad)
	if ce, isCE := coreerrors.AsContainerError(err); !isCE || ce.Description != want {
		t.Errorf("unresolvable in the second value: err = %v, want the ContainerError %q", err, want)
	}
	if err := checkContainerSubstitution(env, "", bad); err == nil || err.Error() != "'${containerEnv}' can not be resolved because no environment variable name is given." {
		t.Errorf("without a configuration file: err = %v", err)
	}
}

func TestExecDockerArgs(t *testing.T) {
	got := execDockerArgs(false, "node", "/workspaces/app", map[string]string{"K": "v"}, []string{"K=cli"}, "cid", []string{"echo", "hi"})
	want := []string{"exec", "-i", "-u", "node", "-w", "/workspaces/app", "-e", "K=v", "-e", "K=cli", "cid", "echo", "hi"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
	got = execDockerArgs(true, "", "", nil, nil, "cid", []string{"sh"})
	if want := []string{"exec", "-it", "cid", "sh"}; !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestExecOutput(t *testing.T) {
	stdout, stderr := execOutput("json", log.Null)
	if rw, ok := stdout.(*rawLogWriter); !ok || stderr != stdout || rw.logger == nil {
		t.Errorf("json: stdout %T, stderr %T, want one raw log writer for both", stdout, stderr)
	}
	stdout, stderr = execOutput("text", log.Null)
	if stdout != os.Stdout || stderr != os.Stderr {
		t.Errorf("text: output must pass through to the process streams")
	}
}

func TestExecRunError(t *testing.T) {
	if err := execRunError(nil); err != nil {
		t.Errorf("success: %v", err)
	}
	runErr := exec.Command("sh", "-c", "exit 3").Run()
	if got := exitCode(execRunError(runErr)); got != 3 {
		t.Errorf("exit 3: code = %d", got)
	}
	if got := exitCode(execRunError(errors.New("docker: not found"))); got != 1 {
		t.Errorf("could not run: code = %d, want 1", got)
	}
}
