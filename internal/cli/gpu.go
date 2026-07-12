package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/devcontainers/cli/internal/docker"
)

// checkGPUAvailability determines if GPU should be enabled based on the flag value.
func checkGPUAvailability(ctx context.Context, gpuFlag string, dockerClient *docker.Client) bool {
	switch gpuFlag {
	case "all":
		return true
	case "none":
		return false
	default: // "detect"
		// Match the TS CLI: the nvidia runtime is present only when docker info
		// reports "nvidia-container-runtime". When absent, the Go template renders
		// "<no value>", which is non-empty — so an emptiness check wrongly enabled
		// GPUs and made `up` fail on hosts without nvidia.
		res, err := dockerClient.Run(ctx, "info", "-f", "{{.Runtimes.nvidia}}")
		return err == nil && strings.Contains(string(res.Stdout), "nvidia-container-runtime")
	}
}

// gpuRequested interprets hostRequirements.gpu (bool | "optional" | object) with
// JS truthiness, matching the TS CLI: false/null/absent → no GPU; true, "optional"
// or an object → GPU requested.
func gpuRequested(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "false"
}
