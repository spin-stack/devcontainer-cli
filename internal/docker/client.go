package docker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/devcontainers/cli/internal/exec"
	"github.com/devcontainers/cli/internal/log"
)

// Client wraps the Docker CLI binary. It shells out to `docker` rather
// than using the Docker API/SDK, matching the TS CLI behavior.
type Client struct {
	DockerPath string
	Env        []string
	Log        log.Log
	// Runner is the seam over process execution. When nil, a default OS-backed
	// runner is used. Tests inject a fake to avoid shelling out.
	Runner exec.Runner
}

// NewClient creates a Docker CLI client.
func NewClient(dockerPath string, env []string, logger log.Log) *Client {
	if dockerPath == "" {
		dockerPath = "docker"
	}
	return &Client{
		DockerPath: dockerPath,
		Env:        env,
		Log:        logger,
	}
}

// ExecResult holds output from a docker command.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// runner returns the configured Runner, or a default OS-backed one carrying the
// client's Env.
func (c *Client) runner() exec.Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return exec.OSRunner{Env: c.Env}
}

// Run executes a docker command and captures output.
func (c *Client) Run(args ...string) (*ExecResult, error) {
	c.Log.Write(fmt.Sprintf("Run: %s %s", c.DockerPath, strings.Join(args, " ")), log.LevelTrace)

	stdout, stderr, exitCode, err := c.runner().Run(context.Background(), c.DockerPath, args...)
	if err != nil {
		return nil, fmt.Errorf("exec docker: %w", err)
	}

	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}, nil
}

// Build runs `docker build` or `docker buildx build`. When opts.Env is set, those
// entries are appended to the subprocess environment (used to point DOCKER_CONFIG
// at a temporary credentials directory for private base-image pulls / --push /
// --cache-to, without mutating the ambient environment).
func (c *Client) Build(opts BuildOptions) (*ExecResult, error) {
	args := c.buildArgs(opts)
	// Secret values reach BuildKit through the subprocess environment (referenced
	// by `--secret id=KEY,env=KEY`), never on the command line.
	extraEnv := append(append([]string{}, opts.Env...), opts.Secrets...)
	if len(extraEnv) > 0 {
		return c.runWithEnv(extraEnv, args...)
	}
	return c.Run(args...)
}

// runWithEnv is Run with extra environment entries appended for this invocation.
func (c *Client) runWithEnv(extraEnv []string, args ...string) (*ExecResult, error) {
	c.Log.Write(fmt.Sprintf("Run: %s %s", c.DockerPath, strings.Join(args, " ")), log.LevelTrace)

	var r exec.Runner = exec.OSRunner{Env: append(append([]string{}, c.Env...), extraEnv...)}
	if c.Runner != nil {
		r = c.Runner // tests inject a fake; env is irrelevant there
	}
	stdout, stderr, exitCode, err := r.Run(context.Background(), c.DockerPath, args...)
	if err != nil {
		return nil, fmt.Errorf("exec docker: %w", err)
	}
	return &ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}, nil
}

// BuildOptions configures a docker build.
type BuildOptions struct {
	Dockerfile  string
	ContextPath string
	Tags        []string
	Target      string
	BuildArgs   map[string]string
	CacheFrom   []string
	Labels      []string
	NoCache     bool
	Pull        bool
	ExtraArgs   []string // additional --build-context, etc.
	Env         []string // extra env for the subprocess (e.g. DOCKER_CONFIG=...)
	Secrets     []string // build secrets as "KEY=VALUE"; passed to buildx as --secret id=KEY,env=KEY with KEY=VALUE in the subprocess env

	// Buildx-specific
	UseBuildx bool
	Platform  string
	Push      bool
	Output    string
	CacheTo   string
}

// buildxCacheToInlineRe matches a buildx cache spec that is itself an inline
// cache exporter (type=inline), mirroring TS isBuildxCacheToInline.
var buildxCacheToInlineRe = regexp.MustCompile(`(?i)type\s*=\s*inline`)

// isBuildxCacheToInline reports whether a --cache-to spec is an inline exporter.
func isBuildxCacheToInline(cacheTo string) bool {
	return cacheTo != "" && buildxCacheToInlineRe.MatchString(cacheTo)
}

func (c *Client) buildArgs(opts BuildOptions) []string {
	var args []string

	if opts.UseBuildx {
		args = append(args, "buildx", "build")
		if opts.Platform != "" {
			args = append(args, "--platform", opts.Platform)
		}
		if opts.Push {
			args = append(args, "--push")
		} else if opts.Output != "" {
			args = append(args, "--output", opts.Output)
		} else {
			args = append(args, "--load")
		}
		if opts.CacheTo != "" {
			args = append(args, "--cache-to", opts.CacheTo)
		}
		// Inline cache is redundant (and rejected) when --cache-to is itself an
		// inline cache exporter; match TS, which skips the build-arg in that case.
		if !isBuildxCacheToInline(opts.CacheTo) {
			args = append(args, "--build-arg", "BUILDKIT_INLINE_CACHE=1")
		}
		// Build secrets (buildx-only): the value is read from the subprocess env
		// (set in Build), so only the id/env reference appears on the command line.
		for _, s := range opts.Secrets {
			if i := strings.IndexByte(s, '='); i > 0 {
				key := s[:i]
				args = append(args, "--secret", "id="+key+",env="+key)
			}
		}
	} else {
		args = append(args, "build")
	}

	if opts.Dockerfile != "" {
		args = append(args, "-f", opts.Dockerfile)
	}
	for _, tag := range opts.Tags {
		args = append(args, "-t", tag)
	}
	if opts.Target != "" {
		args = append(args, "--target", opts.Target)
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
		if opts.UseBuildx {
			args = append(args, "--pull")
		}
	}
	for _, cf := range opts.CacheFrom {
		args = append(args, "--cache-from", cf)
	}
	for k, v := range opts.BuildArgs {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}
	for _, l := range opts.Labels {
		args = append(args, "--label", l)
	}
	args = append(args, opts.ExtraArgs...)

	if opts.ContextPath != "" {
		args = append(args, opts.ContextPath)
	} else {
		args = append(args, ".")
	}

	return args
}

// Tag runs `docker tag`.
func (c *Client) Tag(source, target string) error {
	res, err := c.Run("tag", source, target)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("docker tag failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

// BuildKitInfo holds BuildKit version detection results.
type BuildKitInfo struct {
	Available bool
	Version   string
}

// DetectBuildKit checks if BuildKit is available via `docker buildx version`.
func (c *Client) DetectBuildKit() *BuildKitInfo {
	res, err := c.Run("buildx", "version")
	if err != nil || res.ExitCode != 0 {
		return &BuildKitInfo{Available: false}
	}
	version := strings.TrimSpace(string(res.Stdout))
	return &BuildKitInfo{
		Available: true,
		Version:   version,
	}
}

// BuilderInfo holds information about the active buildx builder.
type BuilderInfo struct {
	Name   string
	Driver string
}

// DetectActiveBuilder returns information about the currently active buildx builder.
func (c *Client) DetectActiveBuilder() *BuilderInfo {
	res, err := c.Run("buildx", "inspect")
	if err != nil || res.ExitCode != 0 {
		return &BuilderInfo{}
	}
	info := &BuilderInfo{}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name:") && info.Name == "" {
			info.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		}
		if strings.HasPrefix(line, "Driver:") && info.Driver == "" {
			info.Driver = strings.TrimSpace(strings.TrimPrefix(line, "Driver:"))
		}
	}
	return info
}

// FindDockerDriverBuilder finds the name of a buildx builder using the "docker" driver
// that matches the current Docker context. This builder can access local images.
func (c *Client) FindDockerDriverBuilder() string {
	// Get the current Docker context
	ctxRes, _ := c.Run("context", "inspect", "--format", "{{.Name}}")
	currentContext := strings.TrimSpace(string(ctxRes.Stdout))

	res, err := c.Run("buildx", "ls")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	// Parse output: each builder starts at column 0 with NAME, followed by DRIVER
	// Prefer builder matching the current context, fall back to any docker driver builder
	fallback := ""
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && !strings.HasPrefix(fields[0], " ") {
			name := strings.TrimSuffix(fields[0], "*")
			driver := fields[1]
			if driver == "docker" {
				// Prefer builder matching current context
				if name == currentContext {
					return name
				}
				if fallback == "" {
					fallback = name
				}
			}
		}
	}
	return fallback
}
