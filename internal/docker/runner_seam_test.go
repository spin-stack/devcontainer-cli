package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// fakeRunner records the command it was asked to run and returns canned output,
// standing in for a real process so Client.Run can be tested without docker. When
// err is set it is returned as the run error (binary-not-found / cancelled style).
// When a stream writer is provided it mirrors a live process by writing the canned
// output there too, so streaming behavior is exercised through the seam.
type fakeRunner struct {
	gotName   string
	gotArgs   []string
	gotStream io.Writer
	stdout    []byte
	stderr    []byte
	code      int
	err       error
}

func (f *fakeRunner) Run(ctx context.Context, stream io.Writer, name string, args ...string) ([]byte, []byte, int, error) {
	f.gotName = name
	f.gotArgs = args
	f.gotStream = stream
	if stream != nil && f.err == nil {
		_, _ = stream.Write(f.stdout)
		_, _ = stream.Write(f.stderr)
	}
	return f.stdout, f.stderr, f.code, f.err
}

// TestRunUsesRunnerSeam proves docker.Client.Run routes through the
// injected exec.Runner instead of shelling out.
func TestRunUsesRunnerSeam(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("hello"), stderr: []byte("warn"), code: 7}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}

	res, err := c.Run(t.Context(), "ps", "-a")
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

// TestRunPropagatesRunnerError proves that a process failure from the injected
// exec.Runner (binary not found, cancelled, ...) surfaces to the caller as a
// wrapped error rather than a bogus success.
func TestRunPropagatesRunnerError(t *testing.T) {
	sentinel := errors.New("exec: \"docker\": executable file not found in $PATH")
	fr := &fakeRunner{err: sentinel}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}

	res, err := c.Run(t.Context(), "ps")
	if err == nil {
		t.Fatal("expected error from failing runner, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil on runner error", res)
	}
}

// TestBuildWithEnvRoutesThroughRunner proves Build honors opts.Env without
// disturbing arg assembly (the DOCKER_CONFIG env only affects the real OSRunner;
// an injected fake still receives the same args and returns its canned result).
func TestBuildWithEnvRoutesThroughRunner(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("ok"), code: 0}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}
	res, err := c.Build(t.Context(), BuildOptions{ContextPath: ".", Env: []string{"DOCKER_CONFIG=/tmp/x"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "ok" {
		t.Fatalf("result = %+v", res)
	}
	if len(fr.gotArgs) == 0 || fr.gotArgs[0] != "build" {
		t.Fatalf("args = %v, want a plain `build ...`", fr.gotArgs)
	}
}

// TestBuildStreamsProgressToWriter proves Build opts into streaming: with a
// ProgressWriter set, the child's output reaches it live (through the seam, so
// the fake exercises it) while still being captured in the result.
func TestBuildStreamsProgressToWriter(t *testing.T) {
	fr := &fakeRunner{stderr: []byte("#1 building\n#2 done\n"), code: 0}
	var progress bytes.Buffer
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr, ProgressWriter: &progress}

	res, err := c.Build(t.Context(), BuildOptions{ContextPath: "."})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if fr.gotStream == nil {
		t.Fatal("Build did not pass a stream writer to the runner")
	}
	if got := progress.String(); !strings.Contains(got, "#1 building") || !strings.Contains(got, "#2 done") {
		t.Errorf("progress = %q, want the build output streamed live", got)
	}
	// The captured copy is still returned (used for the failure message).
	if got := string(res.Stderr); got != "#1 building\n#2 done\n" {
		t.Errorf("captured stderr = %q", got)
	}
}

// TestBuildWithoutProgressWriterDoesNotStream proves streaming is opt-in: no
// ProgressWriter means the runner receives a nil stream and nothing leaks out.
func TestBuildWithoutProgressWriterDoesNotStream(t *testing.T) {
	fr := &fakeRunner{stderr: []byte("#1 building\n"), code: 0}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr} // no ProgressWriter

	if _, err := c.Build(t.Context(), BuildOptions{ContextPath: "."}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if fr.gotStream != nil {
		t.Errorf("Build streamed despite no ProgressWriter (stream = %v)", fr.gotStream)
	}
}

// TestRunNeverStreams proves plain Run() never streams, even when a
// ProgressWriter is configured (only Build/compose build+up opt in).
func TestRunNeverStreams(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("x")}
	var progress bytes.Buffer
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr, ProgressWriter: &progress}

	if _, err := c.Run(t.Context(), "ps"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fr.gotStream != nil {
		t.Errorf("Run passed a stream writer; only Build should stream")
	}
	if progress.Len() != 0 {
		t.Errorf("Run wrote to ProgressWriter: %q", progress.String())
	}
}

// TestComposeBuildStreamsButConfigDoesNot proves compose build opts into
// streaming while `compose config` never does — its stdout is JSON the CLI
// parses, so streaming it would be both useless and corrupting.
func TestComposeBuildStreamsButConfigDoesNot(t *testing.T) {
	rrC := &recordingRunner{responses: []cannedResponse{{stdout: []byte("{}"), code: 0}}}
	var cfgProgress bytes.Buffer
	cc := newComposeV2(rrC, []int{2, 24, 0})
	cc.ProgressWriter = &cfgProgress
	if _, err := cc.Config(t.Context(), []string{"c.yml"}, ""); err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfgProgress.Len() != 0 {
		t.Errorf("compose config streamed to progress: %q", cfgProgress.String())
	}

	rrB := &recordingRunner{responses: []cannedResponse{{stderr: []byte("#1 building\n"), code: 0}}}
	var buildProgress bytes.Buffer
	cb := newComposeV2(rrB, []int{2, 24, 0})
	cb.ProgressWriter = &buildProgress
	if err := cb.Build(t.Context(), []string{"c.yml"}, "", nil, nil, false); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(buildProgress.String(), "#1 building") {
		t.Errorf("compose build did not stream to progress: %q", buildProgress.String())
	}
}
