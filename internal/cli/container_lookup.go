package cli

import (
	"context"
	"fmt"
)

// containerLister is the narrow seam resolveContainerID needs (satisfied by
// *docker.EngineClient), so the lookup is unit-testable without a daemon.
type containerLister interface {
	ListContainers(ctx context.Context, all bool, labelFilters []string) ([]string, error)
}

// resolveContainerID finds a workspace's dev container. Explicit idLabels win.
// Otherwise it prefers the [local_folder, config_file] label filter so that
// multiple devcontainer.json configs in one project (e.g. .devcontainer/frontend
// and .devcontainer/backend, each its own container) resolve to the RIGHT one,
// and falls back to local_folder alone for a container created without a
// config_file label (older runs / --id-label-only) — mirroring the TS CLI's
// old/new id-label scheme. Returns "" when nothing matches.
func resolveContainerID(ctx context.Context, engine containerLister, workspaceFolder, configFile string, idLabels []string) string {
	if len(idLabels) > 0 {
		if ids, _ := engine.ListContainers(ctx, true, idLabels); len(ids) > 0 {
			return ids[0]
		}
		return ""
	}
	if workspaceFolder == "" {
		return ""
	}
	local := fmt.Sprintf("devcontainer.local_folder=%s", resolvePath(workspaceFolder))
	if configFile != "" {
		labels := []string{local, fmt.Sprintf("devcontainer.config_file=%s", configFile)}
		if ids, _ := engine.ListContainers(ctx, true, labels); len(ids) > 0 {
			return ids[0]
		}
	}
	if ids, _ := engine.ListContainers(ctx, true, []string{local}); len(ids) > 0 {
		return ids[0]
	}
	return ""
}
