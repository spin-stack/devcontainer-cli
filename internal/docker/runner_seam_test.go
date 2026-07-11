package docker

import (
	"context"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// fakeRunner records the command it was asked to run and returns canned output,
// standing in for a real process so Client.Run can be tested without docker.
type fakeRunner struct {
	gotName string
	gotArgs []string
	stdout  []byte
	stderr  []byte
	code    int
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.gotName = name
	f.gotArgs = args
	return f.stdout, f.stderr, f.code, nil
}

// TestRunUsesRunnerSeam proves docker.Client.Run routes through the
// injected exec.Runner instead of shelling out.
func TestRunUsesRunnerSeam(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("hello"), stderr: []byte("warn"), code: 7}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}

	res, err := c.Run("ps", "-a")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if fr.gotName != "docker" || len(fr.gotArgs) != 2 || fr.gotArgs[0] != "ps" || fr.gotArgs[1] != "-a" {
		t.Fatalf("runner got name=%q args=%v", fr.gotName, fr.gotArgs)
	}
	if string(res.Stdout) != "hello" || string(res.Stderr) != "warn" || res.ExitCode != 7 {
		t.Fatalf("result = %+v, want stdout=hello stderr=warn code=7", res)
	}
}
