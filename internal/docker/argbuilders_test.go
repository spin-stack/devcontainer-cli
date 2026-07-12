package docker

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// recordingRunner is an exec.Runner that records every invocation and replies
// with canned output. It supports multi-call functions (e.g. builder detection
// that shells out several times) by consuming a queue of responses; once the
// queue is exhausted it returns a zero-value success.
type recordingRunner struct {
	calls     []recordedCall
	responses []cannedResponse
}

type recordedCall struct {
	name string
	args []string
}

type cannedResponse struct {
	stdout []byte
	stderr []byte
	code   int
}

func (r *recordingRunner) Run(_ context.Context, stream io.Writer, name string, args ...string) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	if len(r.responses) > 0 {
		resp := r.responses[0]
		r.responses = r.responses[1:]
		if stream != nil {
			_, _ = stream.Write(resp.stdout)
			_, _ = stream.Write(resp.stderr)
		}
		return resp.stdout, resp.stderr, resp.code, nil
	}
	return nil, nil, 0, nil
}

func (r *recordingRunner) last() recordedCall {
	if len(r.calls) == 0 {
		return recordedCall{}
	}
	return r.calls[len(r.calls)-1]
}

// --- docker build / tag argument routing ---

// TestBuild_RoutesArgsThroughRunner proves Client.Build feeds the exact
// buildArgs slice to the runner seam (name = docker path, args = build args).
func TestBuild_RoutesArgsThroughRunner(t *testing.T) {
	rr := &recordingRunner{}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}

	_, err := c.Build(t.Context(), BuildOptions{
		Dockerfile:  "Dockerfile",
		ContextPath: "/ctx",
		Tags:        []string{"img:1"},
	})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	got := rr.last()
	if got.name != "docker" {
		t.Errorf("runner name = %q, want docker", got.name)
	}
	want := []string{"build", "-f", "Dockerfile", "-t", "img:1", "/ctx"}
	if !reflect.DeepEqual(got.args, want) {
		t.Errorf("build args =\n  %v\nwant\n  %v", got.args, want)
	}
}

func TestTag_Args(t *testing.T) {
	rr := &recordingRunner{}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}

	if err := c.Tag(t.Context(), "src:latest", "dst:latest"); err != nil {
		t.Fatalf("Tag error: %v", err)
	}
	want := []string{"tag", "src:latest", "dst:latest"}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("tag args = %v, want %v", rr.last().args, want)
	}
}

// TestTag_NonZeroExitFails proves Tag surfaces a docker failure exit code.
func TestTag_NonZeroExitFails(t *testing.T) {
	rr := &recordingRunner{responses: []cannedResponse{{stderr: []byte("no such image"), code: 1}}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	if err := c.Tag(t.Context(), "a", "b"); err == nil {
		t.Fatal("expected error on non-zero exit")
	}
}

// --- buildx detection / builder selection parsing ---

func TestDetectBuildKit(t *testing.T) {
	// Available: buildx version returns 0 with a version banner.
	rr := &recordingRunner{responses: []cannedResponse{{stdout: []byte("github.com/docker/buildx v0.12.0\n"), code: 0}}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	info := c.DetectBuildKit(t.Context())
	if !info.Available || info.Version != "github.com/docker/buildx v0.12.0" {
		t.Errorf("DetectBuildKit = %+v", info)
	}
	if want := []string{"buildx", "version"}; !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("args = %v, want %v", rr.last().args, want)
	}

	// Unavailable: non-zero exit.
	rr2 := &recordingRunner{responses: []cannedResponse{{code: 1}}}
	c2 := &Client{DockerPath: "docker", Log: log.Null, Runner: rr2}
	if c2.DetectBuildKit(t.Context()).Available {
		t.Error("expected unavailable on non-zero exit")
	}
}

func TestDetectActiveBuilder(t *testing.T) {
	out := "Name:   default\nDriver: docker\nNodes:\n"
	rr := &recordingRunner{responses: []cannedResponse{{stdout: []byte(out), code: 0}}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	info := c.DetectActiveBuilder(t.Context())
	if info.Name != "default" || info.Driver != "docker" {
		t.Errorf("DetectActiveBuilder = %+v", info)
	}
	if want := []string{"buildx", "inspect"}; !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("args = %v, want %v", rr.last().args, want)
	}
}

// TestFindDockerDriverBuilder exercises the two-call sequence (context inspect,
// then buildx ls) and the parsing that prefers the builder matching the current
// context, falling back to any docker-driver builder.
func TestFindDockerDriverBuilder(t *testing.T) {
	lsOut := "" +
		"NAME/NODE          DRIVER/ENDPOINT   STATUS\n" +
		"remote*            docker-container  running\n" +
		"default            docker            running\n" +
		"mycontext          docker            running\n"

	// Current context "mycontext" matches a docker-driver builder → returned
	// even though "default" (also docker driver) appears first.
	rr := &recordingRunner{responses: []cannedResponse{
		{stdout: []byte("mycontext\n"), code: 0}, // context inspect
		{stdout: []byte(lsOut), code: 0},         // buildx ls
	}}
	c := &Client{DockerPath: "docker", Log: log.Null, Runner: rr}
	if got := c.FindDockerDriverBuilder(t.Context()); got != "mycontext" {
		t.Errorf("FindDockerDriverBuilder = %q, want mycontext", got)
	}

	// No context match → fall back to first docker-driver builder ("default").
	rr2 := &recordingRunner{responses: []cannedResponse{
		{stdout: []byte("other\n"), code: 0},
		{stdout: []byte(lsOut), code: 0},
	}}
	c2 := &Client{DockerPath: "docker", Log: log.Null, Runner: rr2}
	if got := c2.FindDockerDriverBuilder(t.Context()); got != "default" {
		t.Errorf("fallback = %q, want default", got)
	}
}

// --- compose argument construction ---

// newComposeV2 builds a v2 ComposeClient wired to a recording runner. Command
// is ["docker","compose"], so Run prepends "compose" and calls name="docker".
func newComposeV2(rr *recordingRunner, version []int) *ComposeClient {
	return &ComposeClient{
		Command: []string{"docker", "compose"},
		Version: version,
		Log:     log.Null,
		Runner:  rr,
	}
}

func TestComposeRun_V2PrependsSubcommand(t *testing.T) {
	rr := &recordingRunner{}
	c := newComposeV2(rr, []int{2, 24, 0})
	if _, err := c.Run(t.Context(), "ps"); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	got := rr.last()
	if got.name != "docker" {
		t.Errorf("name = %q, want docker", got.name)
	}
	if want := []string{"compose", "ps"}; !reflect.DeepEqual(got.args, want) {
		t.Errorf("args = %v, want %v", got.args, want)
	}
}

func TestComposeRun_V1NoSubcommand(t *testing.T) {
	rr := &recordingRunner{}
	c := &ComposeClient{Command: []string{"docker-compose"}, Log: log.Null, Runner: rr}
	if _, err := c.Run(t.Context(), "ps"); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	got := rr.last()
	if got.name != "docker-compose" {
		t.Errorf("name = %q, want docker-compose", got.name)
	}
	if want := []string{"ps"}; !reflect.DeepEqual(got.args, want) {
		t.Errorf("args = %v, want %v", got.args, want)
	}
}

func TestComposeBuild_Args(t *testing.T) {
	rr := &recordingRunner{}
	c := newComposeV2(rr, []int{2, 24, 0})
	err := c.Build(
		t.Context(),
		[]string{"docker-compose.yml", "docker-compose.override.yml"},
		".env",
		[]string{"--progress", "plain"},
		[]string{"app"},
		true, // noCache
	)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	want := []string{"compose",
		"-f", "docker-compose.yml",
		"-f", "docker-compose.override.yml",
		"--env-file", ".env",
		"--progress", "plain",
		"build",
		"--no-cache",
		"app",
	}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("build args =\n  %v\nwant\n  %v", rr.last().args, want)
	}
}

func TestComposeBuild_NoCacheOmittedNoServices(t *testing.T) {
	rr := &recordingRunner{}
	c := newComposeV2(rr, []int{2, 24, 0})
	if err := c.Build(t.Context(), []string{"c.yml"}, "", nil, nil, false); err != nil {
		t.Fatalf("Build error: %v", err)
	}
	want := []string{"compose", "-f", "c.yml", "build"}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("build args = %v, want %v", rr.last().args, want)
	}
}

func TestComposeUp_Args(t *testing.T) {
	rr := &recordingRunner{}
	c := newComposeV2(rr, []int{2, 24, 0})
	err := c.Up(
		t.Context(),
		[]string{"docker-compose.yml"},
		".env",
		[]string{"--profile", "dev"},
		"myproject",
		[]string{"app", "db"},
		true, // noRecreate
	)
	if err != nil {
		t.Fatalf("Up error: %v", err)
	}
	want := []string{"compose",
		"-f", "docker-compose.yml",
		"--env-file", ".env",
		"--project-name", "myproject",
		"--profile", "dev",
		"up", "-d",
		"--no-recreate",
		"app", "db",
	}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("up args =\n  %v\nwant\n  %v", rr.last().args, want)
	}
}

func TestComposeUp_NoProjectNoRecreate(t *testing.T) {
	rr := &recordingRunner{}
	c := newComposeV2(rr, []int{2, 24, 0})
	if err := c.Up(t.Context(), []string{"c.yml"}, "", nil, "", nil, false); err != nil {
		t.Fatalf("Up error: %v", err)
	}
	want := []string{"compose", "-f", "c.yml", "up", "-d"}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("up args = %v, want %v", rr.last().args, want)
	}
}

func TestComposeConfig_ArgsAndParse(t *testing.T) {
	rr := &recordingRunner{responses: []cannedResponse{
		{stdout: []byte(`{"services":{"app":{"image":"x"}}}`), code: 0},
	}}
	c := newComposeV2(rr, []int{2, 24, 0})
	cfg, err := c.Config(t.Context(), []string{"c.yml"}, ".env")
	if err != nil {
		t.Fatalf("Config error: %v", err)
	}
	want := []string{"compose",
		"-f", "c.yml",
		"--env-file", ".env",
		"config", "--format", "json",
	}
	if !reflect.DeepEqual(rr.last().args, want) {
		t.Errorf("config args =\n  %v\nwant\n  %v", rr.last().args, want)
	}
	if _, ok := cfg["services"]; !ok {
		t.Errorf("parsed config missing services: %v", cfg)
	}
}

// TestComposeBuild_NonZeroExitFails proves a compose failure exit code is
// surfaced as an error rather than swallowed.
func TestComposeBuild_NonZeroExitFails(t *testing.T) {
	rr := &recordingRunner{responses: []cannedResponse{{stderr: []byte("boom"), code: 2}}}
	c := newComposeV2(rr, []int{2, 24, 0})
	if err := c.Build(t.Context(), []string{"c.yml"}, "", nil, nil, false); err == nil {
		t.Fatal("expected error on non-zero compose exit")
	}
}
