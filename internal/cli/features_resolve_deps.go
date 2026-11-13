package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/stringify"
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

			ws := resolvePath(workspaceFolder)
			loadResult, err := config.LoadDevContainerConfig(ws, "", "")
			if err != nil {
				return err
			}

			cfg := loadResult.Config
			userFeatures := features.UserFeaturesToArray(cfg.Features, cfg.FeatureOptionOrder)
			if len(userFeatures) == 0 {
				logger.Write(fmt.Sprintf("Could not parse features object in configuration '%s'", cfg.ConfigFilePath), log.LevelError)
				return &coreerrors.ExitCodeError{Code: 1}
			}

			ociClient := newOCIClient(cmd.Context(), logger)

			var lockfile *features.Lockfile
			if cfg.ConfigFilePath != "" {
				lockfile, _, err = features.ReadLockfile(cfg.ConfigFilePath)
				if err != nil {
					fmt.Fprintln(out.Stderr(), errorInspect(err))
					return &coreerrors.ExitCodeError{Code: 1, Err: err}
				}
			}
			basePath := filepath.Dir(cfg.ConfigFilePath)

			processFeature := newMetadataProcessFeature(ociClient, logger, localFeatureRoots{basePath, ws}, lockfile)
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
					if opts, err = features.JSValue(fs.Features[0].Value, fs.Features[0].ValueOrder); err != nil {
						return err
					}
				}
				entries = append(entries, installEntry{ID: id, Options: opts})
			}

			fmt.Fprintln(out.Stdout(), features.GenerateMermaidDiagram(graph))

			data, _ := stringify.JSON(map[string]any{"installOrder": entries}, "  ")
			fmt.Fprintln(out.Stdout(), string(data))
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder.")
	cmd.Flags().StringVar(&logLevel, "log-level", "error", "Log level.")

	return cmd
}

func errorInspect(err error) string {
	if lerr, ok := errors.AsType[*features.LockfileError](err); ok {
		return lerr.Inspect()
	}
	return err.Error()
}
