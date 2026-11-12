package docker

import (
	"context"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/exec"
	"github.com/devcontainers/cli/internal/log"
)

type Client struct {
	DockerPath     string
	Env            []string
	Log            log.Logger
	Runner         exec.Runner
	ProgressWriter io.Writer
}

func NewClient(dockerPath string, env []string, logger log.Logger) *Client {
	if dockerPath == "" {
		dockerPath = "docker"
	}
	return &Client{
		DockerPath: dockerPath,
		Env:        env,
		Log:        logger,
	}
}

type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

func (c *Client) runner(extraEnv []string) exec.Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return exec.OSRunner{Env: append(append([]string{}, c.Env...), extraEnv...)}
}

func (c *Client) run(ctx context.Context, stream bool, extraEnv []string, args ...string) (*ExecResult, error) {
	c.Log.Write(fmt.Sprintf("Run: %s %s", c.DockerPath, strings.Join(args, " ")), log.LevelTrace)

	var live io.Writer
	if stream {
		live = c.ProgressWriter
	}
	stdout, stderr, exitCode, err := c.runner(extraEnv).Run(ctx, live, c.DockerPath, args...)
	if err != nil {
		return nil, fmt.Errorf("exec docker: %w", err)
	}
	return &ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}, nil
}

func (c *Client) Run(ctx context.Context, args ...string) (*ExecResult, error) {
	return c.run(ctx, false, nil, args...)
}

func (c *Client) Build(ctx context.Context, opts BuildOptions) (*ExecResult, error) {
	args := c.buildArgs(opts)
	extraEnv := append(append([]string{}, opts.Env...), opts.Secrets...)
	return c.run(ctx, true, extraEnv, args...)
}

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
	ExtraArgs   []string
	Env         []string
	Secrets     []string
	FileSecrets map[string]string

	UseBuildx bool
	Platform  string
	Push      bool
	Output    string
	CacheTo   string
}

var buildxCacheToInlineRe = regexp.MustCompile(`(?i)type\s*=\s*inline`)

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
		if !isBuildxCacheToInline(opts.CacheTo) {
			args = append(args, "--build-arg", "BUILDKIT_INLINE_CACHE=1")
		}
		for _, s := range opts.Secrets {
			if i := strings.IndexByte(s, '='); i > 0 {
				key := s[:i]
				args = append(args, "--secret", "id="+key+",env="+key)
			}
		}
		for _, id := range slices.Sorted(maps.Keys(opts.FileSecrets)) {
			args = append(args, "--secret", "id="+id+",src="+opts.FileSecrets[id])
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

func (c *Client) Tag(ctx context.Context, source, target string) error {
	res, err := c.Run(ctx, "tag", source, target)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("docker tag failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

type BuildKitInfo struct {
	Available bool
	Version   string
}

func (c *Client) DetectBuildKit(ctx context.Context) *BuildKitInfo {
	res, err := c.Run(ctx, "buildx", "version")
	if err != nil || res.ExitCode != 0 {
		return &BuildKitInfo{Available: false}
	}
	version := strings.TrimSpace(string(res.Stdout))
	return &BuildKitInfo{
		Available: true,
		Version:   version,
	}
}

type BuilderInfo struct {
	Name   string
	Driver string
}

func (c *Client) DetectActiveBuilder(ctx context.Context) *BuilderInfo {
	res, err := c.Run(ctx, "buildx", "inspect")
	if err != nil || res.ExitCode != 0 {
		return &BuilderInfo{}
	}
	info := &BuilderInfo{}
	for line := range strings.SplitSeq(string(res.Stdout), "\n") {
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

func (c *Client) FindDockerDriverBuilder(ctx context.Context) string {
	ctxRes, _ := c.Run(ctx, "context", "inspect", "--format", "{{.Name}}")
	currentContext := strings.TrimSpace(string(ctxRes.Stdout))

	res, err := c.Run(ctx, "buildx", "ls")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	fallback := ""
	for line := range strings.SplitSeq(string(res.Stdout), "\n") {
		if strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			name := strings.TrimSuffix(fields[0], "*")
			driver := fields[1]
			if driver == "docker" {
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
