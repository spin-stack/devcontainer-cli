package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
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
			// 0.88: workspace-folder defaults to cwd when not provided.
			if workspaceFolder == "" {
				workspaceFolder, _ = os.Getwd()
			}

			logger := log.New(log.Options{
				Level:  log.MapLogLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

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

			ociClient := oci.NewClient(logger, osEnvMap())

			// Read the lockfile (if any) so tarball/OCI resolution can pin digests.
			var lockfile *features.Lockfile
			if cfg.ConfigFilePath != "" {
				lockfile, _, _ = features.ReadLockfile(cfg.ConfigFilePath)
			}
			basePath := filepath.Dir(cfg.ConfigFilePath)

			// Build the dependency graph once (with dependency edges and
			// overrideFeatureInstallOrder priorities), then derive both the mermaid
			// diagram and the install order from it — the single-builder path
			// (RW-002). The previous edge-less node list ignored transitive
			// dependencies in the install order.
			processFeature := newMetadataProcessFeature(ociClient, logger, basePath, lockfile)
			graph, err := features.BuildDependencyGraph(logger, processFeature, userFeatures, cfg.OverrideFeatureInstallOrder, lockfile)
			if err != nil {
				return err
			}

			order, err := features.ComputeInstallationOrder(logger, graph, nil)
			if err != nil {
				return err
			}

			type installEntry struct {
				ID      string      `json:"id"`
				Options interface{} `json:"options"`
			}

			var entries []installEntry
			for _, fs := range order {
				src := fs.SourceInfo
				id := src.UserFeatureID()
				if ociSrc, ok := src.(*features.OCISource); ok {
					id = fmt.Sprintf("%s/%s/%s@%s", ociSrc.Registry, ociSrc.Namespace, ociSrc.ID, fs.ComputedDigest)
				}
				var opts interface{}
				if len(fs.Features) > 0 {
					opts = fs.Features[0].Value
				}
				entries = append(entries, installEntry{ID: id, Options: opts})
			}

			// The TS CLI prints the mermaid dependency graph to stdout before the
			// install-order JSON.
			fmt.Fprintln(out.Stdout(), features.GenerateMermaidDiagram(graph))

			data, _ := json.MarshalIndent(map[string]interface{}{"installOrder": entries}, "", "  ")
			fmt.Fprintln(out.Stdout(), string(data))
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder.")
	cmd.Flags().StringVar(&logLevel, "log-level", "error", "Log level.")

	return cmd
}
