package docker

import (
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name string
		opts BuildOptions
		want []string
	}{
		{
			name: "Simple",
			opts: BuildOptions{
				Dockerfile:  "Dockerfile",
				ContextPath: "/project",
				Tags:        []string{"myimage:latest"},
			},
			want: []string{"build", "-f", "Dockerfile", "-t", "myimage:latest", "/project"},
		},
		{
			name: "Buildx",
			opts: BuildOptions{
				UseBuildx:   true,
				Platform:    "linux/amd64",
				Push:        false,
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
				Tags:        []string{"img:1"},
			},
			want: []string{"buildx", "build", "--platform", "linux/amd64", "--load", "--build-arg", "BUILDKIT_INLINE_CACHE=1", "-f", "Dockerfile", "-t", "img:1", "."},
		},
		{
			name: "BuildxPush",
			opts: BuildOptions{
				UseBuildx:   true,
				Push:        true,
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			// --push present, --load absent
			want: []string{"buildx", "build", "--push", "--build-arg", "BUILDKIT_INLINE_CACHE=1", "-f", "Dockerfile", "."},
		},
		{
			name: "BuildxOutput",
			opts: BuildOptions{
				UseBuildx:   true,
				Output:      "type=docker",
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			// --output present, --load absent
			want: []string{"buildx", "build", "--output", "type=docker", "--build-arg", "BUILDKIT_INLINE_CACHE=1", "-f", "Dockerfile", "."},
		},
		{
			name: "CacheTo",
			opts: BuildOptions{
				UseBuildx:   true,
				CacheTo:     "type=registry,ref=cache",
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"buildx", "build", "--load", "--cache-to", "type=registry,ref=cache", "--build-arg", "BUILDKIT_INLINE_CACHE=1", "-f", "Dockerfile", "."},
		},
		{
			name: "NoCache",
			opts: BuildOptions{
				NoCache:     true,
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"build", "-f", "Dockerfile", "--no-cache", "."},
		},
		{
			name: "NoCacheBuildx",
			opts: BuildOptions{
				UseBuildx:   true,
				NoCache:     true,
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"buildx", "build", "--load", "--build-arg", "BUILDKIT_INLINE_CACHE=1", "-f", "Dockerfile", "--no-cache", "--pull", "."},
		},
		{
			name: "Labels",
			opts: BuildOptions{
				Labels:      []string{"key1=val1", "key2=val2"},
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"build", "-f", "Dockerfile", "--label", "key1=val1", "--label", "key2=val2", "."},
		},
		{
			name: "Target",
			opts: BuildOptions{
				Target:      "dev",
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"build", "-f", "Dockerfile", "--target", "dev", "."},
		},
		{
			name: "BuildArgMap",
			opts: BuildOptions{
				BuildArgs:   map[string]string{"NODE_VERSION": "18"},
				Dockerfile:  "Dockerfile",
				ContextPath: ".",
			},
			want: []string{"build", "-f", "Dockerfile", "--build-arg", "NODE_VERSION=18", "."},
		},
	}

	c := NewClient("docker", nil, log.Null)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.buildArgs(tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestNewClient_DefaultPath(t *testing.T) {
	c := NewClient("", nil, log.Null)
	if c.DockerPath != "docker" {
		t.Errorf("default path = %q", c.DockerPath)
	}
}
