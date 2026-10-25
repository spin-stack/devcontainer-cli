package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

// rawCapture records the raw text written to a log.
type rawCapture struct {
	log.Logger
	mu  sync.Mutex
	raw strings.Builder
}

func (c *rawCapture) Raw(text string, _ ...log.Level) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw.WriteString(text)
}

func (c *rawCapture) Write(string, ...log.Level) {}

// initializeCommand runs in the workspace folder, and its stdout and stderr go
// to the log, never to the CLI's stdout, which holds only the JSON result: TS
// runInitializeCommand runs it through runCommand with the CLI host's cwd and
// writes every chunk with output.raw.
func TestRunInitializeCommandLogsItsOutputFromTheWorkspace(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := &rawCapture{Logger: log.Null}
	cmd := cmdFromJSON(t, `"pwd -P; echo to-stderr >&2"`)
	if err := RunInitializeCommand(context.Background(), logger, cmd, config.HostSubContext{Platform: "linux", LocalWorkspaceFolder: ws}); err != nil {
		t.Fatal(err)
	}
	got := logger.raw.String()
	if !strings.Contains(got, ws+"\n") || !strings.Contains(got, "to-stderr\n") {
		t.Errorf("logged %q, want the workspace folder %s from stdout and to-stderr from stderr", got, ws)
	}
}
