package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

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
				log: log.New(log.Options{Writer: io.Discard}),
				opts: &buildOpts{
					dockerPath:        "/nonexistent/docker-binary",
					dockerComposePath: tt.dockerComposePath,
				},
			}
			cfg := &config.DevContainer{
				ConfigFilePath:    "/tmp/devcontainer.json",
				Service:           "app",
				DockerComposeFile: config.StringOrStrings{"docker-compose.yml"},
			}

			_, err := r.buildCompose(t.Context(), cfg, &config.LoadResult{}, false)
			if err == nil {
				t.Fatal("expected compose-client detection error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain custom compose path %q", err.Error(), tt.wantInErr)
			}
		})
	}
}
