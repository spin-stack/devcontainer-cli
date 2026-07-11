package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

// TestBuildComposePathWiring verifies that the `build` command threads
// --docker-compose-path into docker.NewComposeClient. When the call site
// hardcodes "" instead, the operator-supplied path could never appear in the
// compose-client detection error ("neither 'docker compose' nor '<path>'
// found"). The test is hermetic: dockerPath points at a nonexistent binary so
// the `docker compose` (v2) probe always fails regardless of the host, forcing
// NewComposeClient down the custom-path branch.
func TestBuildComposePathWiring(t *testing.T) {
	tests := []struct {
		name              string
		dockerComposePath string
		wantInErr         string
	}{
		{
			name:              "custom path surfaces in error",
			dockerComposePath: "/nonexistent/xyz",
			wantInErr:         "/nonexistent/xyz",
		},
		{
			name:              "different custom path surfaces in error",
			dockerComposePath: "/opt/custom/compose-bin",
			wantInErr:         "/opt/custom/compose-bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &buildRunner{
				ctx: context.Background(),
				log: log.New(log.Options{Writer: io.Discard}),
				opts: &buildOpts{
					// Force the `docker compose` (v2) probe to fail hermetically,
					// independent of whether Docker is installed on the host.
					dockerPath:        "/nonexistent/docker-binary",
					dockerComposePath: tt.dockerComposePath,
				},
			}
			cfg := &config.DevContainerConfig{
				ConfigFilePath:    "/tmp/devcontainer.json",
				Service:           "app",
				DockerComposeFile: config.StringOrStrings{"docker-compose.yml"},
			}

			_, err := r.buildCompose(cfg, false)
			if err == nil {
				t.Fatal("expected compose-client detection error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain custom compose path %q", err.Error(), tt.wantInErr)
			}
		})
	}
}
