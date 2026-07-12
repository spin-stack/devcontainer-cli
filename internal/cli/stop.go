package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

type stopOpts struct {
	workspaceFolder string
	idLabels        []string
	containerID     string
	dockerPath      string
	removeVolumes   bool // down only
}

func newStopCmd() *cobra.Command {
	var opts stopOpts
	cmd := &cobra.Command{
		Use:   "stop [path]",
		Short: "Stop a workspace's dev container without removing it",
		Long: `Gracefully stop the dev container for a workspace so it stops consuming
resources, leaving it and its data intact to be restarted with 'up'. For a
Docker Compose config all of the project's services are stopped.

Useful on a remote host: pause the container without tearing anything down.

Go-only command; not part of the upstream @devcontainers/cli.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && opts.workspaceFolder == "" {
				opts.workspaceFolder = args[0]
			}
			return runStopOrDown(cmd.Context(), outputFor(cmd), opts, false)
		},
	}
	addStopFlags(cmd, &opts)
	return cmd
}

func newDownCmd() *cobra.Command {
	var opts stopOpts
	cmd := &cobra.Command{
		Use:   "down [path]",
		Short: "Stop and remove a workspace's dev container",
		Long: `Stop and remove the dev container for a workspace (Compose: 'docker compose
down'). Data in named volumes persists unless --remove-volumes is given; a later
'up' then provisions a fresh container.

Go-only command; not part of the upstream @devcontainers/cli.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && opts.workspaceFolder == "" {
				opts.workspaceFolder = args[0]
			}
			return runStopOrDown(cmd.Context(), outputFor(cmd), opts, true)
		},
	}
	addStopFlags(cmd, &opts)
	cmd.Flags().BoolVar(&opts.removeVolumes, "remove-volumes", false, "Also remove named volumes (Compose). Destructive: deletes persisted data.")
	return cmd
}

func addStopFlags(cmd *cobra.Command, opts *stopOpts) {
	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path (defaults to [path] or the current directory).")
	f.StringArrayVar(&opts.idLabels, "id-label", nil, "id label(s) of the target container (name=value); repeatable.")
	f.StringVar(&opts.containerID, "container-id", "", "Target container id directly.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
}

func runStopOrDown(ctx context.Context, out Output, opts stopOpts, remove bool) error {
	if opts.workspaceFolder == "" && len(opts.idLabels) == 0 && opts.containerID == "" {
		opts.workspaceFolder, _ = os.Getwd()
	}

	logger := log.New(log.Options{Writer: out.Stderr(), Format: "text"})
	engine, err := docker.NewEngineClient(logger)
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("Docker engine: %v", err))
	}
	defer engine.Close()

	id := resolveWorkspaceContainer(ctx, engine, opts)
	if id == "" {
		// Nothing to do — already gone. Not an error (idempotent).
		return writeSuccessJSON(out, map[string]interface{}{"outcome": "success", "result": "no-container-found"})
	}

	// Compose services carry the project label; operate on the whole project so
	// sibling services (db, cache, …) are handled too, not just the dev container.
	project := ""
	if insp, ierr := engine.InspectContainer(ctx, id); ierr == nil && insp.Config != nil {
		project = insp.Config.Labels["com.docker.compose.project"]
	}

	action := "stopped"
	if remove {
		action = "removed"
	}

	if project != "" {
		compose, cerr := docker.NewComposeClient(opts.dockerPath, "", nil, logger)
		if cerr != nil {
			return writeErrorResult(out, cerr.Error())
		}
		if remove {
			err = compose.Down(ctx, nil, project, opts.removeVolumes)
		} else {
			err = compose.Stop(ctx, nil, project)
		}
	} else {
		err = engine.StopContainer(ctx, id)
		if err == nil && remove {
			err = engine.RemoveContainer(ctx, id)
		}
	}
	if err != nil {
		return writeErrorResult(out, fmt.Sprintf("%s: %v", action, err))
	}

	return writeSuccessJSON(out, map[string]interface{}{
		"outcome":     "success",
		"containerId": id,
		"result":      action,
	})
}

// resolveWorkspaceContainer finds the dev container for a workspace: a directly
// given --container-id, else the first container matching --id-label or the
// workspace's devcontainer.local_folder label. all=true so a stopped container
// is still found (down after a prior stop).
func resolveWorkspaceContainer(ctx context.Context, engine *docker.EngineClient, opts stopOpts) string {
	if opts.containerID != "" {
		return opts.containerID
	}
	labels := opts.idLabels
	if len(labels) == 0 && opts.workspaceFolder != "" {
		labels = []string{fmt.Sprintf("devcontainer.local_folder=%s", resolvePath(opts.workspaceFolder))}
	}
	if len(labels) == 0 {
		return ""
	}
	ids, err := engine.ListContainers(ctx, true, labels)
	if err != nil || len(ids) == 0 {
		return ""
	}
	return ids[0]
}
