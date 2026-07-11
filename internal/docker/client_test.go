package docker

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestBuildArgs_Simple(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		Dockerfile:  "Dockerfile",
		ContextPath: "/project",
		Tags:        []string{"myimage:latest"},
	})

	if args[0] != "build" {
		t.Errorf("expected 'build', got %q", args[0])
	}
	assertContains(t, args, "-f", "Dockerfile")
	assertContains(t, args, "-t", "myimage:latest")
	if args[len(args)-1] != "/project" {
		t.Errorf("last arg should be context path, got %q", args[len(args)-1])
	}
}

func TestBuildArgs_Buildx(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		Platform:    "linux/amd64",
		Push:        false,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
		Tags:        []string{"img:1"},
	})

	if args[0] != "buildx" || args[1] != "build" {
		t.Errorf("expected 'buildx build', got %v", args[:2])
	}
	assertContains(t, args, "--platform", "linux/amd64")
	assertContains(t, args, "--load")
	assertContains(t, args, "--build-arg", "BUILDKIT_INLINE_CACHE=1")
}

func TestBuildArgs_BuildxPush(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		Push:        true,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--push")
	// --load should NOT be present when --push is
	for _, a := range args {
		if a == "--load" {
			t.Error("--load should not be present with --push")
		}
	}
}

func TestBuildArgs_BuildxOutput(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		Output:      "type=docker",
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--output", "type=docker")
	for _, a := range args {
		if a == "--load" {
			t.Error("--load should not be present with --output")
		}
	}
}

func TestBuildArgs_CacheTo(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		CacheTo:     "type=registry,ref=cache",
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--cache-to", "type=registry,ref=cache")
}

func TestBuildArgs_NoCache(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		NoCache:     true,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--no-cache")
}

func TestBuildArgs_NoCacheBuildx(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		NoCache:     true,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--no-cache")
	assertContains(t, args, "--pull")
}

func TestBuildArgs_Labels(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		Labels:      []string{"key1=val1", "key2=val2"},
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--label", "key1=val1")
	assertContains(t, args, "--label", "key2=val2")
}

func TestBuildArgs_Target(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		Target:      "dev",
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	assertContains(t, args, "--target", "dev")
}

func TestBuildArgs_BuildArgMap(t *testing.T) {
	c := NewClient("docker", nil, log.Null)
	args := c.buildArgs(BuildOptions{
		BuildArgs:   map[string]string{"NODE_VERSION": "18"},
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
	})

	found := false
	for _, a := range args {
		if strings.Contains(a, "NODE_VERSION=18") {
			found = true
		}
	}
	if !found {
		t.Error("expected build arg NODE_VERSION=18")
	}
}

func TestNewClient_DefaultPath(t *testing.T) {
	c := NewClient("", nil, log.Null)
	if c.DockerPath != "docker" {
		t.Errorf("default path = %q", c.DockerPath)
	}
}

func assertContains(t *testing.T, args []string, values ...string) {
	t.Helper()
	for i, arg := range args {
		if arg == values[0] {
			if len(values) == 1 {
				return
			}
			if i+1 < len(args) && args[i+1] == values[1] {
				return
			}
		}
	}
	t.Errorf("args %v does not contain %v", args, values)
}
