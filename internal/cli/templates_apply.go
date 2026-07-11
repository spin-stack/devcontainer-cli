package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/templates"
	"github.com/spf13/cobra"
)

func realTemplatesApplyCmd() *cobra.Command {
	var (
		workspaceFolder string
		templateID      string
		templateArgs    string
		featuresJSON    string
		logLevel        string
		tmpDir          string
		omitPaths       string
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a template to the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if templateID == "" {
				return fmt.Errorf("Missing required argument: --template-id")
			}

			logger := log.New(log.Options{
				Level:  log.MapLogLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			ws := resolvePath(workspaceFolder)

			// Parse template args
			var options map[string]string
			if templateArgs != "" {
				if err := jsonc.Unmarshal([]byte(templateArgs), &options); err != nil {
					// TS emits a fixed message (no parser detail) so both the
					// non-object and malformed-JSON paths canonicalize identically.
					return fmt.Errorf("Invalid template arguments provided.")
				}
			}
			if options == nil {
				options = map[string]string{}
			}

			// Parse omit paths
			var omit []string
			if omitPaths != "" {
				jsonc.Unmarshal([]byte(omitPaths), &omit)
			}

			// Parse features
			var featureOpts []templates.TemplateFeatureOption
			if featuresJSON != "" && featuresJSON != "[]" {
				if err := json.Unmarshal([]byte(featuresJSON), &featureOpts); err != nil {
					return fmt.Errorf("Invalid features JSON: %w", err)
				}
			}

			ociClient := oci.NewClient(logger, osEnvMap())

			selected := templates.SelectedTemplate{
				ID:        templateID,
				Options:   options,
				Features:  featureOpts,
				OmitPaths: omit,
			}

			files, err := templates.FetchAndApply(templates.ApplyParams{
				OCIClient:       ociClient,
				Logger:          logger,
				Env:             osEnvMap(),
				WorkspaceFolder: ws,
				TmpDir:          tmpDir,
			}, selected)
			if err != nil {
				return err
			}

			data, _ := json.Marshal(map[string]interface{}{"files": files})
			fmt.Fprintln(outputFor(cmd).Stdout(), string(data))
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&workspaceFolder, "workspace-folder", "w", ".", "Target workspace folder.")
	f.StringVarP(&templateID, "template-id", "t", "", "Template OCI reference.")
	f.StringVarP(&templateArgs, "template-args", "a", "{}", "Template arguments JSON.")
	f.StringVarP(&featuresJSON, "features", "f", "[]", "Features to add JSON.")
	f.StringVar(&logLevel, "log-level", "info", "Log level.")
	f.StringVar(&tmpDir, "tmp-dir", "", "Temp directory.")
	f.StringVar(&omitPaths, "omit-paths", "[]", "Paths to omit JSON.")
	return cmd
}
