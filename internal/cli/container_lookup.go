package cli

import (
	"context"
	"fmt"
	"strings"
)

// containerLister is the narrow seam resolveContainerID needs (satisfied by
// *docker.EngineClient), so the lookup is unit-testable without a daemon.
type containerLister interface {
	ListContainers(ctx context.Context, all bool, labelFilters []string) ([]string, error)
}

// containerListError is a failed container lookup, worded as TS's: its
// findDevContainer runs `docker ps -q -a --filter label=…` and the failure is
// "Command failed: <that command>" (commonUtils.ts). The engine's error is
// kept for errors.Is/As.
type containerListError struct {
	labels []string
	err    error
}

func (e *containerListError) Error() string {
	var b strings.Builder
	b.WriteString("Command failed: docker ps -q -a")
	for _, l := range e.labels {
		b.WriteString(" --filter label=" + l)
	}
	return b.String()
}

func (e *containerListError) Unwrap() error { return e.err }

// listContainers is ListContainers with a failure as a containerListError.
func listContainers(ctx context.Context, engine containerLister, labels []string) ([]string, error) {
	ids, err := engine.ListContainers(ctx, true, labels)
	if err != nil {
		return nil, &containerListError{labels: labels, err: err}
	}
	return ids, nil
}

// resolveContainerID finds a workspace's dev container. Explicit idLabels win.
// Otherwise it prefers the [local_folder, config_file] label filter so that
// multiple devcontainer.json configs in one project (e.g. .devcontainer/frontend
// and .devcontainer/backend, each its own container) resolve to the RIGHT one,
// and falls back to local_folder alone for a container created without a
// config_file label (older runs / --id-label-only) — mirroring the TS CLI's
// old/new id-label scheme. Returns "" when nothing matches, and the first
// lookup that fails as a containerListError, as TS's findContainerAndIdLabels
// throws.
func resolveContainerID(ctx context.Context, engine containerLister, workspaceFolder, configFile string, idLabels []string) (string, error) {
	if len(idLabels) > 0 {
		ids, err := listContainers(ctx, engine, idLabels)
		if err != nil || len(ids) == 0 {
			return "", err
		}
		return ids[0], nil
	}
	if workspaceFolder == "" {
		return "", nil
	}
	local := fmt.Sprintf("devcontainer.local_folder=%s", resolvePath(workspaceFolder))
	if configFile != "" {
		labels := []string{local, fmt.Sprintf("devcontainer.config_file=%s", configFile)}
		ids, err := listContainers(ctx, engine, labels)
		if err != nil {
			return "", err
		}
		if len(ids) > 0 {
			return ids[0], nil
		}
	}
	ids, err := listContainers(ctx, engine, []string{local})
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

// failingLister is the containerLister without a Docker engine: every lookup
// fails with the engine's error.
type failingLister struct{ err error }

func (f failingLister) ListContainers(context.Context, bool, []string) ([]string, error) {
	return nil, f.err
}
