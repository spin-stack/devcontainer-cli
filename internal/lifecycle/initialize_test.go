package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

// cmdFromJSON builds a LifecycleCommand the way the loader does: by unmarshalling
// its JSON form (string | []string | object), since raw is unexported.
func cmdFromJSON(t *testing.T, raw string) *config.LifecycleCommand {
	t.Helper()
	var c config.LifecycleCommand
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("build command from %s: %v", raw, err)
	}
	return &c
}

func TestRunInitializeCommand(t *testing.T) {
	// LocalWorkspaceFolder is also the child's working directory (cmd.Dir), so it
	// must be a real path.
	hostSub := config.HostSubContext{Platform: "linux", LocalWorkspaceFolder: t.TempDir()}

	tests := []struct {
		name    string
		cmd     *config.LifecycleCommand
		wantErr bool
	}{
		{"nil command", nil, false},
		{"empty command", cmdFromJSON(t, `null`), false},
		{"string form", cmdFromJSON(t, `"true"`), false},
		{"string form with substitution", cmdFromJSON(t, `"true # ${localWorkspaceFolder}"`), false},
		{"string form non-zero exit", cmdFromJSON(t, `"false"`), true},
		{"array form", cmdFromJSON(t, `["true"]`), false},
		{"array form with substitution", cmdFromJSON(t, `["true", "${localWorkspaceFolder}"]`), false},
		{"object form parallel", cmdFromJSON(t, `{"a": "true", "b": "true"}`), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunInitializeCommand(context.Background(), log.Null, tt.cmd, hostSub)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// A ${localWorkspaceFolder} in the command is resolved against hostSub before the
// command runs; the substituted value must reach the child process.
func TestRunInitializeCommandSubstitutesLocalWorkspaceFolder(t *testing.T) {
	dir := t.TempDir()
	hostSub := config.HostSubContext{Platform: "linux", LocalWorkspaceFolder: dir}
	// `test -d <dir>` succeeds only if the substituted path is a real directory,
	// proving the substitution happened (an unresolved ${...} would fail).
	cmd := cmdFromJSON(t, `"test -d ${localWorkspaceFolder}"`)
	if err := RunInitializeCommand(context.Background(), log.Null, cmd, hostSub); err != nil {
		t.Fatalf("substitution not applied to command: %v", err)
	}
}
