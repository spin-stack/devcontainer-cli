package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	osexec "os/exec"
	"strconv"
	"strings"

	"github.com/devcontainers/cli/internal/exec"
	"github.com/devcontainers/cli/internal/log"
)

// ComposeClient wraps Docker Compose CLI (v1 or v2).
type ComposeClient struct {
	// Command is ["docker", "compose"] (Compose v2).
	Command []string
	Version []int // parsed version e.g. [2, 24, 0]
	Env     []string
	Log     log.Logger
	// Runner is the seam over process execution. When nil, a default OS-backed
	// runner is used.
	Runner exec.Runner
	// ProgressWriter, when set, receives `docker compose build`/`up` output live
	// so the user sees progress as it happens. Leave nil to keep output buffered
	// (e.g. under --log-format json). `compose config` never streams — its stdout
	// is JSON that the CLI parses.
	ProgressWriter io.Writer
}

// NewComposeClient detects Compose v2 (`docker compose`) and returns a client.
// Compose v1 (`docker-compose`) is out of scope for this CLI; --docker-compose-path
// is accepted for flag parity but only surfaces in the not-found error.
func NewComposeClient(dockerPath, composePath string, env []string, logger log.Logger) (*ComposeClient, error) {
	if dockerPath == "" {
		dockerPath = "docker"
	}
	if out, err := osexec.Command(dockerPath, "compose", "version", "--short").Output(); err == nil {
		return &ComposeClient{
			Command: []string{dockerPath, "compose"},
			Version: parseComposeVersion(strings.TrimSpace(string(out))),
			Env:     env,
			Log:     logger,
		}, nil
	}
	if composePath != "" {
		return nil, fmt.Errorf("'docker compose' (v2) not found; --docker-compose-path %q is Compose v1, which is unsupported", composePath)
	}
	return nil, fmt.Errorf("'docker compose' (v2) not found")
}

// Run executes a compose command and captures output (no live streaming).
func (c *ComposeClient) Run(ctx context.Context, args ...string) (*ExecResult, error) {
	return c.run(ctx, false, args...)
}

// run executes a compose command. When stream is true and ProgressWriter is set,
// the child's output is also forwarded live through the runner. ctx cancels the
// subprocess.
func (c *ComposeClient) run(ctx context.Context, stream bool, args ...string) (*ExecResult, error) {
	fullArgs := append(c.Command[1:], args...)

	// Emit a "Run:" start/stop pair around the subprocess, like the TS CLI wraps
	// every external command. (Docker Engine operations go through the SDK, not a
	// subprocess, so they don't produce "Run:" events — a deliberate difference
	// from the TS CLI, which shells out to the docker CLI for everything.)
	runLine := fmt.Sprintf("Run: %s %s", c.Command[0], strings.Join(fullArgs, " "))
	startTS := c.Log.Start(runLine, log.LevelDebug)

	var runner exec.Runner = exec.OSRunner{Env: c.Env}
	if c.Runner != nil {
		runner = c.Runner
	}
	var live io.Writer
	if stream {
		live = c.ProgressWriter
	}
	stdout, stderr, exitCode, err := runner.Run(ctx, live, c.Command[0], fullArgs...)
	c.Log.Stop(runLine, startTS, log.LevelDebug)
	if err != nil {
		return nil, fmt.Errorf("exec compose: %w", err)
	}

	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}, nil
}

// Config runs `docker compose config` and returns parsed YAML.
func (c *ComposeClient) Config(ctx context.Context, composeFiles []string, envFile string) (map[string]interface{}, error) {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, "config", "--format", "json")

	res, err := c.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("compose config failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}

	var config map[string]interface{}
	if err := json.Unmarshal(res.Stdout, &config); err != nil {
		return nil, fmt.Errorf("parse compose config: %w", err)
	}
	return config, nil
}

// Build runs `docker compose build`.
func (c *ComposeClient) Build(ctx context.Context, composeFiles []string, envFile string, globalArgs, services []string, noCache bool) error {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, globalArgs...)
	args = append(args, "build")
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, services...)

	res, err := c.run(ctx, true, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose build failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// Up runs `docker compose up`.
func (c *ComposeClient) Up(ctx context.Context, composeFiles []string, envFile string, globalArgs []string, projectName string, services []string, noRecreate bool) error {
	args := c.buildGlobalArgs(composeFiles, envFile)
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	args = append(args, globalArgs...)
	args = append(args, "up", "-d")
	if noRecreate {
		args = append(args, "--no-recreate")
	}
	args = append(args, services...)

	res, err := c.run(ctx, true, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose up failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// Stop stops the project's services without removing them (`docker compose
// stop`), so `up` can restart them. Preserves containers, networks and volumes.
func (c *ComposeClient) Stop(ctx context.Context, composeFiles []string, projectName string) error {
	args := c.buildGlobalArgs(composeFiles, "")
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	args = append(args, "stop")

	res, err := c.Run(ctx, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose stop failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// Down stops and removes the project's containers and networks (`docker compose
// down`). When removeVolumes is set, named volumes are removed too (destructive).
func (c *ComposeClient) Down(ctx context.Context, composeFiles []string, projectName string, removeVolumes bool) error {
	args := c.buildGlobalArgs(composeFiles, "")
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	args = append(args, "down")
	if removeVolumes {
		args = append(args, "--volumes")
	}

	res, err := c.Run(ctx, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose down failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// SupportsAdditionalContexts returns true if compose version >= 2.17.0.
func (c *ComposeClient) SupportsAdditionalContexts() bool {
	if len(c.Version) < 2 {
		return false
	}
	// 2.17.0 added additional_contexts
	if c.Version[0] > 2 {
		return true
	}
	if c.Version[0] == 2 && c.Version[1] >= 17 {
		return true
	}
	return false
}

// UsesNewProjectNames returns true if compose version >= 1.21.0
// (allowed hyphens and underscores in project names).
func (c *ComposeClient) UsesNewProjectNames() bool {
	if len(c.Version) < 2 {
		return true // optimistic default
	}
	if c.Version[0] > 1 {
		return true
	}
	if c.Version[0] == 1 && c.Version[1] >= 21 {
		return true
	}
	return false
}

func (c *ComposeClient) buildGlobalArgs(composeFiles []string, envFile string) []string {
	var args []string
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	return args
}

// parseComposeVersion extracts the first N[.N[.N]] version found in s (e.g. from
// "Docker Compose version v2.15.1"), up to three numeric components.
func parseComposeVersion(s string) []int {
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return nil
	}
	var version []int
	for len(version) < 3 {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(s[i:j])
		version = append(version, n)
		if j >= len(s) || s[j] != '.' {
			break
		}
		i = j + 1
	}
	return version
}

// ToProjectName sanitizes a name for use as a compose project name.
// Matches TS toProjectName() behavior.
func ToProjectName(basename string, newProjectName bool) string {
	// Compose < 1.21 keeps only [a-z0-9]; >= 1.21 also allows [_-].
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case newProjectName && (r == '_' || r == '-'):
			return r
		}
		return -1
	}, strings.ToLower(basename))
}
