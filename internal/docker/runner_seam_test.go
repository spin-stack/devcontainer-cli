package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/log"
)

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
	if got := string(res.Stderr); got != "#1 building\n#2 done\n" {
		t.Errorf("captured stderr = %q", got)
	}
}

func TestBuildWithoutProgressWriterDoesNotStream(t *testing.T) {
	fr := &fakeRunner{stderr: []byte("#1 building\n"), code: 0}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: fr}

	if _, err := c.Build(t.Context(), BuildOptions{ContextPath: "."}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if fr.gotStream != nil {
		t.Errorf("Build streamed despite no ProgressWriter (stream = %v)", fr.gotStream)
	}
}

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

func TestComposeConfigReadsEveryProfileFromYAML(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("name: proj\nservices:\n  app:\n    image: debian\n    ports:\n      - target: 80\n    sysctls:\n      1: x\n")}
	c := &ComposeClient{Command: []string{"docker", "compose"}, Log: log.Null, Runner: fr}
	cfg, err := c.Config(t.Context(), []string{"/ws/a.yml"}, "/ws/.env")
	if err != nil {
		t.Fatal(err)
	}
	app := cfg["services"].(map[string]any)["app"].(map[string]any)
	if app["image"] != "debian" || app["ports"].([]any)[0].(map[string]any)["target"] != float64(80) || app["sysctls"].(map[string]any)["1"] != "x" {
		t.Errorf("config = %v", cfg)
	}
	fr.stdout = nil
	if cfg, err := c.Config(t.Context(), []string{"/ws/a.yml"}, ""); err != nil || len(cfg) != 0 {
		t.Errorf("empty output: config = %v, err = %v", cfg, err)
	}
}

func TestComposeConfigFailureIsTheReferenceContainerError(t *testing.T) {
	fr := &fakeRunner{stderr: []byte("project name must not be empty"), code: 1}
	c := &ComposeClient{Command: []string{"docker", "compose"}, Log: log.Null, Runner: fr}
	_, err := c.Config(t.Context(), []string{"/ws/a.yml"}, "")
	out := coreerrors.ToErrorOutput(fmt.Errorf("wrapped: %w", err))
	if out.Description != "An error occurred retrieving the Docker Compose configuration." || out.Message != "Command failed: docker compose -f /ws/a.yml --profile * config" {
		t.Errorf("error = %+v", out)
	}
	fr.code, fr.stdout = 0, []byte("services: [")
	if _, err := c.Config(t.Context(), []string{"/ws/a.yml"}, ""); err == nil || !strings.Contains(coreerrors.ToErrorOutput(err).Message, "parse compose config") {
		t.Errorf("malformed output: err = %v", err)
	}
}

func TestComposeConfigLogsTheCommandAtInfoAndItsOutput(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("services:\n  app:\n    image: alpine\n")}
	lgr, events := jsonLog()
	c := &ComposeClient{Command: []string{"docker", "compose"}, Log: lgr, Runner: fr}
	if _, err := c.Config(t.Context(), []string{"/ws/a.yml"}, ""); err != nil {
		t.Fatal(err)
	}
	run := "Run: docker compose -f /ws/a.yml --profile * config"
	want := []string{"start info: " + run, "stop info: " + run, "text debug: services:\n  app:\n    image: alpine\n"}
	if got := events(); !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestComposeConfigLogsAFailureOnce(t *testing.T) {
	fr := &fakeRunner{stdout: []byte("out"), stderr: []byte("err"), code: 1}
	lgr, events := jsonLog()
	c := &ComposeClient{Command: []string{"docker", "compose"}, Log: lgr, Runner: fr}
	if _, err := c.Config(t.Context(), []string{"/ws/a.yml"}, ""); err == nil {
		t.Fatal("Config succeeded")
	}
	run := "Run: docker compose -f /ws/a.yml --profile * config"
	want := []string{"start info: " + run, "stop info: " + run, "text error: outerr"}
	if got := events(); !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
}
