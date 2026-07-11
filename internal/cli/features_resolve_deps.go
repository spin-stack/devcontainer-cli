package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/features"
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
			if workspaceFolder == "" {
				return fmt.Errorf("Missing required argument: --workspace-folder")
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
				fmt.Fprintln(os.Stdout, `{"installOrder":[]}`)
				return nil
			}

			ociClient := oci.NewClient(logger, osEnvMap())

			// Build FNodes from user features
			var nodes []*features.FNode
			for _, uf := range userFeatures {
				resolvedID, _ := features.ResolveFeatureID(uf.UserFeatureID, false)
				ref, err := oci.ParseRef(resolvedID)
				if err != nil {
					continue
				}

				manifest, err := ociClient.FetchManifest(ref, "")
				if err != nil {
					continue
				}

				node := &features.FNode{
					Type:          "user-provided",
					UserFeatureID: uf.UserFeatureID,
					Options:       uf.Options,
					FeatureSet: &features.FeatureSet{
						SourceInfo: &features.OCISource{
							Registry:       ref.Registry,
							Namespace:      ref.Namespace,
							ID:             ref.ID,
							Resource:       ref.Resource,
							Tag:            ref.Tag,
							ManifestDigest: manifest.ContentDigest,
							UserID:         uf.UserFeatureID,
						},
						Features:       []features.Feature{{ID: ref.ID, Version: ref.Tag, Value: uf.Options}},
						ComputedDigest: manifest.ContentDigest,
					},
					DependsOn:     []*features.FNode{},
					InstallsAfter: []*features.FNode{},
				}
				nodes = append(nodes, node)
			}

			order, err := features.ComputeInstallationOrder(logger, nodes, cfg.OverrideFeatureInstallOrder)
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

			out, _ := json.MarshalIndent(map[string]interface{}{"installOrder": entries}, "", "  ")
			fmt.Fprintln(os.Stdout, string(out))
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder.")
	cmd.Flags().StringVar(&logLevel, "log-level", "error", "Log level.")

	return cmd
}
