package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/exec"
	"github.com/devcontainers/cli/internal/log"
)

type ComposeClient struct {
	Command        []string
	Version        []int
	Env            []string
	Log            log.Logger
	Runner         exec.Runner
	ProgressWriter io.Writer
}

func NewComposeClient(dockerPath, composePath string, env []string, logger log.Logger) (*ComposeClient, error) {
	if dockerPath == "" {
		dockerPath = "docker"
	}
	runLine := "Run: " + dockerPath + " compose version --short"
	start := logger.Start(runLine, log.LevelDebug)
	out, err := osexec.Command(dockerPath, "compose", "version", "--short").Output()
	logger.Stop(runLine, start, log.LevelDebug)
	if err == nil {
		version := strings.TrimSpace(string(out))
		logger.Write("Docker Compose version: "+version, log.LevelDebug)
		return &ComposeClient{
			Command: []string{dockerPath, "compose"},
			Version: parseComposeVersion(version),
			Env:     withBakeFSEntitlement(env, os.LookupEnv),
			Log:     logger,
		}, nil
	}
	if composePath != "" {
		return nil, fmt.Errorf("'docker compose' (v2) not found; --docker-compose-path %q is Compose v1, which is unsupported", composePath)
	}
	return nil, fmt.Errorf("'docker compose' (v2) not found")
}

// bakeFSEntitlement turns off bake's filesystem entitlement check for the
// compose processes this CLI starts. Compose builds through bake, and bake on
// buildx 0.38 and later refuses to read a Dockerfile outside the build context
// unless the caller passes --allow=fs.read, which `docker compose build` cannot;
// the Features Dockerfile this CLI writes lives in its cache folder, outside the
// context. The reference passes nothing and fails there ("additional privileges
// requested"); docs/DIVERGENCES.md records the difference.
const bakeFSEntitlement = "BUILDX_BAKE_ENTITLEMENTS_FS"

func withBakeFSEntitlement(env []string, lookup func(string) (string, bool)) []string {
	if _, ok := lookup(bakeFSEntitlement); ok || slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, bakeFSEntitlement+"=") }) {
		return env
	}
	return append(slices.Clone(env), bakeFSEntitlement+"=0")
}

func (c *ComposeClient) Run(ctx context.Context, args ...string) (*ExecResult, error) {
	return c.run(ctx, log.LevelDebug, false, args...)
}

func (c *ComposeClient) run(ctx context.Context, level log.Level, stream bool, args ...string) (*ExecResult, error) {
	fullArgs := append(c.Command[1:], args...)

	// Emit a "Run:" start/stop pair around the subprocess, like the TS CLI wraps
	// every external command. (Docker Engine operations go through the SDK, not a
	// subprocess, so they don't produce "Run:" events — a deliberate difference
	// from the TS CLI, which shells out to the docker CLI for everything.)
	runLine := fmt.Sprintf("Run: %s %s", c.Command[0], strings.Join(fullArgs, " "))
	startTS := c.Log.Start(runLine, level)

	var runner exec.Runner = exec.OSRunner{Env: c.Env}
	if c.Runner != nil {
		runner = c.Runner
	}
	var live io.Writer
	if stream {
		live = c.ProgressWriter
	}
	stdout, stderr, exitCode, err := runner.Run(ctx, live, c.Command[0], fullArgs...)
	c.Log.Stop(runLine, startTS, level)
	if err != nil {
		return nil, fmt.Errorf("exec compose: %w", err)
	}

	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}, nil
}

func (c *ComposeClient) Config(ctx context.Context, composeFiles []string, envFile string) (map[string]any, error) {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, "--profile", "*", "config")

	res, err := c.run(ctx, log.LevelInfo, false, args...)
	if err == nil && res.ExitCode == 0 {
		c.Log.Write(string(res.Stdout))
	}
	if err == nil && res.ExitCode != 0 {
		c.Log.Write(string(res.Stdout)+string(res.Stderr), log.LevelError)
		err = &CommandFailedError{Command: strings.Join(append(slices.Clone(c.Command), args...), " "), ExitCode: res.ExitCode}
	}
	var config map[string]any
	if err == nil {
		config, err = parseComposeConfig(res.Stdout)
	}
	if err != nil {
		return nil, &coreerrors.ContainerError{Description: "An error occurred retrieving the Docker Compose configuration.", OriginalError: err}
	}
	return config, nil
}

func parseComposeConfig(out []byte) (map[string]any, error) {
	var doc any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse compose config: %w", err)
	}
	data, err := json.Marshal(stringKeys(doc))
	if err != nil {
		return nil, fmt.Errorf("parse compose config: %w", err)
	}
	config := map[string]any{}
	if doc != nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("parse compose config: %w", err)
		}
	}
	return config, nil
}

func stringKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = stringKeys(e)
		}
	case map[any]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			m[fmt.Sprint(k)] = stringKeys(e)
		}
		return m
	case []any:
		for i, e := range v {
			v[i] = stringKeys(e)
		}
	}
	return v
}

type CommandFailedError struct {
	Command  string
	ExitCode int
}

func (e *CommandFailedError) Error() string {
	return "Command failed: " + e.Command
}

func (c *ComposeClient) Build(ctx context.Context, composeFiles []string, envFile string, globalArgs, services []string, noCache bool) error {
	args := c.buildGlobalArgs(composeFiles, envFile)
	args = append(args, globalArgs...)
	args = append(args, "build")
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, services...)

	res, err := c.run(ctx, log.LevelDebug, true, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose build failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

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

	res, err := c.run(ctx, log.LevelDebug, true, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("compose up failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

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

func (c *ComposeClient) SupportsAdditionalContexts() bool {
	return !isEarlierVersion(c.Version, []int{2, 17, 0})
}

func (c *ComposeClient) UsesNewProjectNames() bool {
	return len(c.Version) == 0 || !isEarlierVersion(c.Version, []int{1, 21, 0})
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

func parseComposeVersion(s string) []int {
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return nil
	}
	var version []int
	for {
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

func ToProjectName(basename string, newProjectName bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case newProjectName && (r == '_' || r == '-'):
			return r
		}
		return -1 // mutate-exempt: strings.Map drops the rune for any negative value
	}, strings.ToLower(basename))
}

func (c *ComposeClient) DefaultImageName(projectName, service string) string {
	sep := "-"
	if len(c.Version) > 0 && isEarlierVersion(c.Version, []int{2, 8, 0}) {
		sep = "_"
	}
	return projectName + sep + service
}

func isEarlierVersion(left, right []int) bool {
	for i := range max(len(left), len(right)) {
		var l, r int
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l != r {
			return l < r
		}
	}
	return false
}
