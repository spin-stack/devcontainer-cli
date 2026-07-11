package docker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

// ComposeClient wraps Docker Compose CLI (v1 or v2).
type ComposeClient struct {
	// Command is either ["docker-compose"] (v1) or ["docker", "compose"] (v2)
	Command []string
	IsV2    bool
	Version []int // parsed version e.g. [2, 24, 0]
	Env     []string
	Log     log.Log
}

// NewComposeClient detects the compose CLI and returns a client.
func NewComposeClient(dockerPath, composePath string, env []string, logger log.Log) (*ComposeClient, error) {
	if composePath == "" {
		composePath = "docker-compose"
	}
	if dockerPath == "" {
		dockerPath = "docker"
	}

	// Try docker compose (v2) first
	if out, err := exec.Command(dockerPath, "compose", "version", "--short").Output(); err == nil {
		version := strings.TrimSpace(string(out))
		return &ComposeClient{
			Command: []string{dockerPath, "compose"},
			IsV2:    true,
			Version: parseComposeVersion(version),
			Env:     env,
			Log:     logger,
		}, nil
	}

	// Fall back to docker-compose (v1)
	if out, err := exec.Command(composePath, "version", "--short").Output(); err == nil {
		version := strings.TrimSpace(string(out))
		return &ComposeClient{
			Command: []string{composePath},
			IsV2:    false,
			Version: parseComposeVersion(version),
			Env:     env,
			Log:     logger,
		}, nil
	}

	return nil, fmt.Errorf("neither 'docker compose' nor '%s' found", composePath)
}

// Run executes a compose command.
func (c *ComposeClient) Run(args ...string) (*ExecResult, error) {
	fullArgs := append(c.Command[1:], args...)

	// Emit a "Run:" start/stop pair around the subprocess, like the TS CLI wraps
	// every external command. (Docker Engine operations go through the SDK, not a
	// subprocess, so they don't produce "Run:" events — a deliberate difference
	// from the TS CLI, which shells out to the docker CLI for everything.)
	runLine := fmt.Sprintf("Run: %s %s", c.Command[0], strings.Join(fullArgs, " "))
	startTS := c.Log.Start(runLine, log.LevelDebug)

	cmd := exec.Command(c.Command[0], fullArgs...)
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	c.Log.Stop(runLine, startTS, log.LevelDebug)
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec compose: %w", err)
		}
	}

	return &ExecResult{
		Stdout:   []byte(stdout.String()),
		Stderr:   []byte(stderr.String()),
		ExitCode: exitCode,
	}, nil
}

// Config runs `docker compose config` and returns parsed YAML.
func (c *ComposeClient) Config(composeFiles []string, envFile string) (map[string]interface{}, error) {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, "config", "--format", "json")

	res, err := c.Run(args...)
	if err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("compose config failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}

	var config map[string]interface{}
	if err := json.Unmarshal(res.Stdout, &config); err != nil {
		// compose config outputs YAML, but json might work for v2
		// For simplicity we'll handle the raw output
		return nil, fmt.Errorf("parse compose config: %w", err)
	}
	return config, nil
}

// Build runs `docker compose build`.
func (c *ComposeClient) Build(composeFiles []string, envFile string, globalArgs, services []string, noCache bool) error {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, globalArgs...)
	args = append(args, "build")
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, services...)

	res, err := c.Run(args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose build failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// Up runs `docker compose up`.
func (c *ComposeClient) Up(composeFiles []string, envFile string, globalArgs []string, projectName string, services []string, noRecreate bool) error {
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

	res, err := c.Run(args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose up failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// Down runs `docker compose down`.
func (c *ComposeClient) Down(composeFiles []string, envFile string, globalArgs []string, projectName string) error {
	args := c.buildGlobalArgs(composeFiles, envFile)
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	args = append(args, globalArgs...)
	args = append(args, "down")

	res, err := c.Run(args...)
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

var versionPattern = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

func parseComposeVersion(s string) []int {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	var version []int
	for _, part := range m[1:] {
		if part == "" {
			break
		}
		n, _ := strconv.Atoi(part)
		version = append(version, n)
	}
	return version
}

// ToProjectName sanitizes a name for use as a compose project name.
// Matches TS toProjectName() behavior.
func ToProjectName(basename string, newProjectName bool) string {
	lower := strings.ToLower(basename)
	if !newProjectName {
		// Compose < 1.21: only [a-z0-9]
		return regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(lower, "")
	}
	// Compose >= 1.21: [a-z0-9_-]
	return regexp.MustCompile(`[^a-z0-9_-]`).ReplaceAllString(lower, "")
}
