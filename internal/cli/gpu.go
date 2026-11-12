package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/devcontainers/cli/internal/docker"
)

var nvidiaContainerCLIInfo = func(ctx context.Context) (found, gpuPresent bool) {
	path, err := exec.LookPath("nvidia-container-cli")
	if err != nil {
		return false, false
	}
	return true, exec.CommandContext(ctx, path, "info").Run() == nil
}

func checkGPUAvailability(ctx context.Context, gpuFlag string, dockerClient *docker.Client) bool {
	switch gpuFlag {
	case "all":
		return true
	case "none":
		return false
	default:
		res, err := dockerClient.Run(ctx, "info", "-f", "{{.Runtimes.nvidia}}")
		runtimePresent := err == nil && strings.Contains(string(res.Stdout), "nvidia-container-runtime")
		return detectGPU(ctx, runtimePresent)
	}
}

// detectGPU decides whether to enable the GPU in "detect" mode given whether
// docker info reported the nvidia runtime. The nvidia runtime being installed
// does NOT prove a GPU is present: a non-GPU cloud instance can still carry the
// driver/runtime, and the docker-info check then wrongly enables --gpus all,
// failing `up` (upstream #319). When nvidia-container-cli is available, trust
// its more precise probe; when it is absent, keep the TS runtime-only behavior.
func detectGPU(ctx context.Context, runtimePresent bool) bool {
	if !runtimePresent {
		return false
	}
	if found, gpuPresent := nvidiaContainerCLIInfo(ctx); found {
		return gpuPresent
	}
	return true
}

func gpuRequested(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "false"
}
