package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/spf13/cobra"
)

func realFeaturesResolveDepsCmd() *cobra.Command {
	var (
		workspaceFolder string
		logLevel        string
	)

	cmd := &cobra.Command{
		Use:   "resolve-dependencies",
		Short: "Read and resolve dependency graph from a configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := outputFor(cmd)
			logger := log.New(log.Options{
				Level:  log.ParseLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			// 0.88: workspace-folder defaults to cwd when not provided, which
			// resolvePath("") is.
			ws := resolvePath(workspaceFolder)
			loadResult, err := config.LoadDevContainerConfig(ws, "", "")
			if err != nil {
				return err
			}

			cfg := loadResult.Config
			userFeatures := features.UserFeaturesToArray(cfg.Features)
			if len(userFeatures) == 0 {
				// TS parity: no parseable features → stderr error + exit 1
				// (featuresCLI/resolveDependencies.ts:92-93), NOT an empty
				// installOrder on stdout.
				logger.Write(fmt.Sprintf("Could not parse features object in configuration '%s'", cfg.ConfigFilePath), log.LevelError)
				return &coreerrors.ExitCodeError{Code: 1}
			}

			ociClient := newOCIClient(cmd.Context(), logger)

			// Read the lockfile (if any) so tarball/OCI resolution can pin digests.
			// One that cannot be read fails the command as an uncaught error does
			// in TS (runAsyncHandler): console.error(err) on stderr, exit code 1.
			var lockfile *features.Lockfile
			if cfg.ConfigFilePath != "" {
				lockfile, _, err = features.ReadLockfile(cfg.ConfigFilePath)
				if err != nil {
					fmt.Fprintln(out.Stderr(), errorInspect(err))
					return &coreerrors.ExitCodeError{Code: 1, Err: err}
				}
			}
			basePath := filepath.Dir(cfg.ConfigFilePath)

			// Build the dependency graph once (with dependency edges and
			// overrideFeatureInstallOrder priorities), then derive both the mermaid
			// diagram and the install order from it via a single builder. The
			// previous edge-less node list ignored transitive dependencies in
			// the install order.
			processFeature := newMetadataProcessFeature(ociClient, logger, basePath, lockfile)
			graph, err := features.BuildDependencyGraph(logger, processFeature, userFeatures, cfg.OverrideFeatureInstallOrder, lockfile)
			if err != nil {
				return err
			}

			order, err := features.ComputeInstallationOrder(logger, graph)
			if err != nil {
				return err
			}

			type installEntry struct {
				ID      string `json:"id"`
				Options any    `json:"options"`
			}

			var entries []installEntry
			for _, fs := range order {
				src := fs.SourceInfo
				id := src.UserFeatureID()
				if ociSrc, ok := src.(*features.OCISource); ok {
					id = fmt.Sprintf("%s/%s/%s@%s", ociSrc.Registry, ociSrc.Namespace, ociSrc.ID, fs.ComputedDigest)
				}
				var opts any
				if len(fs.Features) > 0 {
					opts = fs.Features[0].Value
				}
				entries = append(entries, installEntry{ID: id, Options: opts})
			}

			// The TS CLI prints the mermaid dependency graph to stdout before the
			// install-order JSON.
			fmt.Fprintln(out.Stdout(), features.GenerateMermaidDiagram(graph))

			data, _ := json.MarshalIndent(map[string]any{"installOrder": entries}, "", "  ")
			fmt.Fprintln(out.Stdout(), string(data))
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder.")
	cmd.Flags().StringVar(&logLevel, "log-level", "error", "Log level.")

	return cmd
}

// errorInspect is what console.error prints for an error the reference
// throws, frames left out: a lockfile error as Node.js shows the JavaScript
// error, any other with its message.
func errorInspect(err error) string {
	if lerr, ok := errors.AsType[*features.LockfileError](err); ok {
		return lerr.Inspect()
	}
	return err.Error()
}
