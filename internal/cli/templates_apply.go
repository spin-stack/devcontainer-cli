package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/stringify"
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
			selected, err := templateSelection(templateID, templateArgs, featuresJSON, omitPaths)
			if err != nil {
				return err
			}

			logger := log.New(log.Options{
				Level:  log.ParseLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			ws := resolvePath(workspaceFolder)
			ociClient := newOCIClient(cmd.Context(), logger)

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

			data, _ := stringify.JSON(map[string]any{"files": files}, "")
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

func templateSelection(templateID, templateArgs, featuresJSON, omitPaths string) (templates.SelectedTemplate, error) {
	if templateID == "" {
		return templates.SelectedTemplate{}, fmt.Errorf("Missing required argument: --template-id")
	}
	var options map[string]string
	if templateArgs != "" {
		if err := jsonc.Unmarshal([]byte(templateArgs), &options); err != nil {
			return templates.SelectedTemplate{}, fmt.Errorf("Invalid template arguments provided.")
		}
	}
	var omit []string
	_ = jsonc.Unmarshal([]byte(omitPaths), &omit)
	var featureOpts []templates.TemplateFeatureOption
	if featuresJSON != "" {
		if err := json.Unmarshal([]byte(featuresJSON), &featureOpts); err != nil {
			return templates.SelectedTemplate{}, fmt.Errorf("Invalid features JSON: %w", err)
		}
	}
	return templates.SelectedTemplate{
		ID:        templateID,
		Options:   options,
		Features:  featureOpts,
		OmitPaths: omit,
	}, nil
}
