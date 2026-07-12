package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/devcontainers/cli/internal/docker"
)

// nvidiaContainerCLIInfo probes for a real, usable GPU via `nvidia-container-cli
// info`. It returns found=false when the tool is not installed (so callers fall
// back to the docker-info check). When found, gpuPresent reflects whether the
// tool reported a GPU: it exits non-zero with "nvml error: driver not loaded"
// when the runtime is installed but no GPU is actually present. Overridable in
// tests.
var nvidiaContainerCLIInfo = func(ctx context.Context) (found, gpuPresent bool) {
	path, err := exec.LookPath("nvidia-container-cli")
	if err != nil {
		return false, false
	}
	return true, exec.CommandContext(ctx, path, "info").Run() == nil
}

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

// gpuRequested interprets hostRequirements.gpu (bool | "optional" | object) with
// JS truthiness, matching the TS CLI: false/null/absent → no GPU; true, "optional"
// or an object → GPU requested.
func gpuRequested(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "false"
}
