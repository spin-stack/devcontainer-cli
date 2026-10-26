package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// The Dockerfile and build context resolve as TS getDockerfilePath and
// getDockerContextPath do: against the configuration's folder, an absolute
// path as it is, and without a context the Dockerfile's own folder.
func TestDockerfileAndContextPaths(t *testing.T) {
	cfgPath := "/ws/.devcontainer/devcontainer.json"
	for _, tc := range []struct {
		name                 string
		build                *config.Build
		legacy, legacyCtx    string
		dockerfile, contextP string
	}{
		{"relative", &config.Build{Dockerfile: "Dockerfile"}, "", "", "/ws/.devcontainer/Dockerfile", "/ws/.devcontainer"},
		{"in a subfolder", &config.Build{Dockerfile: "sub/Dockerfile"}, "", "", "/ws/.devcontainer/sub/Dockerfile", "/ws/.devcontainer/sub"},
		{"above the configuration", &config.Build{Dockerfile: "../Dockerfile", Context: ".."}, "", "", "/ws/Dockerfile", "/ws"},
		{"absolute", &config.Build{Dockerfile: "/elsewhere/x/Dockerfile"}, "", "", "/elsewhere/x/Dockerfile", "/elsewhere/x"},
		{"absolute context", &config.Build{Dockerfile: "Dockerfile", Context: "/ctx/"}, "", "", "/ws/.devcontainer/Dockerfile", "/ctx"},
		{"legacy dockerFile", nil, "/elsewhere/Dockerfile", "", "/elsewhere/Dockerfile", "/elsewhere"},
		{"legacy context", nil, "Dockerfile", "..", "/ws/.devcontainer/Dockerfile", "/ws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.DevContainer{ConfigFilePath: cfgPath, Build: tc.build, DockerFile: tc.legacy, Context: tc.legacyCtx}
			if got := dockerfileConfigPath(cfg); got != tc.dockerfile {
				t.Errorf("Dockerfile = %q, want %q", got, tc.dockerfile)
			}
			if got := dockerContextPath(cfg); got != tc.contextP {
				t.Errorf("context = %q, want %q", got, tc.contextP)
			}
		})
	}
}

// An absolute build.dockerfile is read where it is by the build and by
// --cache-key, not joined onto the configuration's folder.
func TestAbsoluteDockerfileIsReadWhereItIs(t *testing.T) {
	dockerfile := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3 AS base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.DevContainer{ConfigFilePath: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &config.Build{Dockerfile: dockerfile}}
	prep, err := prepareDockerfileBuild(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer prep.Cleanup()
	if prep.Path != dockerfile || prep.StageName != "base" {
		t.Errorf("prep = %q, stage %q; want %q, stage base", prep.Path, prep.StageName, dockerfile)
	}
	key, err := computeCacheKey(&config.LoadResult{Config: cfg}, nil)
	if err != nil || key == "" {
		t.Fatalf("cache key = %q, %v", key, err)
	}
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3.20 AS base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := computeCacheKey(&config.LoadResult{Config: cfg}, nil); err != nil || changed == key {
		t.Errorf("cache key after editing the Dockerfile = %q, %v; want it to change from %q", changed, err, key)
	}
}
