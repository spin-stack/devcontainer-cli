package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func runStopCommand(t *testing.T, cmd *cobra.Command, args ...string) map[string]any {
	t.Helper()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	_ = cmd.ExecuteContext(t.Context())
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON result: %v\n%s", err, out.String())
	}
	return got
}

// stop [path] and down [path] act on the container of the workspace the
// argument names, not the current directory's; --workspace-folder wins over
// the argument.
func TestStopAndDownActOnThePositionalWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cmd    func() *cobra.Command
		action string
	}{{"stop", newStopCmd, "stopped"}, {"down", newDownCmd, "removed"}} {
		t.Run(tc.name, func(t *testing.T) {
			ws, cwd := t.TempDir(), t.TempDir()
			t.Chdir(cwd)
			startFakeEngine(t, map[string]map[string]string{
				"ws":  {"devcontainer.local_folder": ws},
				"cwd": {"devcontainer.local_folder": cwd},
			})
			cli, _ := fakeDockerCLI(t, false, 0)

			got := runStopCommand(t, tc.cmd(), ws, "--docker-path", cli)
			assertResult(t, got, map[string]any{"outcome": "success", "containerId": "ws", "result": tc.action})

			got = runStopCommand(t, tc.cmd(), t.TempDir(), "--workspace-folder", ws, "--docker-path", cli)
			assertResult(t, got, map[string]any{"outcome": "success", "containerId": "ws", "result": tc.action})
		})
	}
}

// --config looks first for the workspace's container of that config file.
func TestStopConfigFlagLooksForThatConfigsContainer(t *testing.T) {
	ws := t.TempDir()
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	engine := startFakeEngine(t, map[string]map[string]string{
		"c1": {"devcontainer.local_folder": ws, "devcontainer.config_file": cfg},
	})
	cli, _ := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{workspaceFolder: ws, configPath: cfg, dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": "stopped"})
	want := "list devcontainer.config_file=" + cfg + ",devcontainer.local_folder=" + ws
	if calls := engine.recorded(); !slices.Contains(calls, want) {
		t.Errorf("engine calls = %v, want %q", calls, want)
	}
}
