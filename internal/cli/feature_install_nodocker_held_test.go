package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

// A Feature's containerEnv is resolved against the host (${localEnv:…}) before
// it is written into the extended image, and the build still reports the
// image's names.
func TestExtendImageWithFeaturesResolvesFeatureHostVars(t *testing.T) {
	roots := devcontainerRoots(t)
	base := roots.configDir
	dir := filepath.Join(base, "envy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(`{"id":"envy","version":"1.0.0","containerEnv":{"FROM_HOST":"${localEnv:HOST_VALUE}"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &fakeDockerRunner{}
	names, err := extendImageWithFeatures(t.Context(), log.Null, fakeDockerClient(r), &docker.EngineClient{API: &fakeDockerAPI{version: "25.0.0"}},
		"base:latest", map[string]any{"./envy": true}, false, []string{"out:1"}, &FeatureBuildOptions{
			FeaturesBasePath: base,
			WorkspaceFolder:  roots.workspaceRoot,
			HostSub:          &config.HostSubContext{Platform: "linux", Env: map[string]string{"HOST_VALUE": "resolved-on-host"}},
		})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"out:1"}) {
		t.Errorf("names = %v, want [out:1]", names)
	}
	var dockerfile string
	for _, c := range r.calls {
		for i, a := range c {
			if a == "-f" && i+1 < len(c) && strings.HasSuffix(c[i+1], "Dockerfile.extended") {
				dockerfile = c[i+1]
				t.Cleanup(func() { os.RemoveAll(c[len(c)-1]) })
			}
		}
	}
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatalf("extended Dockerfile: %v (calls %v)", err, r.calls)
	}
	if !strings.Contains(string(data), "resolved-on-host") || strings.Contains(string(data), "localEnv") {
		t.Errorf("extended Dockerfile = %s, want FROM_HOST resolved from the host", data)
	}
}

// With buildx, the Features build gets the features folder as the
// dev_containers_feature_content_source build context.
func TestExtendImageWithFeaturesBuildContext(t *testing.T) {
	res := extendRun{useBuildx: true}.run(t)
	if res.err != nil {
		t.Fatal(res.err)
	}
	i := slices.Index(res.build, "--build-context")
	if i < 0 || i+1 >= len(res.build) || res.build[i+1] != "dev_containers_feature_content_source="+res.features {
		t.Errorf("build = %v, want --build-context dev_containers_feature_content_source=%s", res.build, res.features)
	}
}

// A tarball download succeeds on every 2xx up to 299 and fails from 300 on,
// and it stops with the caller's context.
func TestDownloadFeatureTarballStatusAndContext(t *testing.T) {
	for status, wantErr := range map[int]bool{299: false, 300: true} {
		_, _, err := downloadFeatureTarball(t.Context(), nil, serveBytes(t, []byte("tgz"), status), nil)
		if (err != nil) != wantErr {
			t.Errorf("HTTP %d: err = %v, want an error: %v", status, err, wantErr)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := downloadFeatureTarball(ctx, nil, serveBytes(t, []byte("tgz"), 200), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: err = %v, want %v", err, context.Canceled)
	}
}
