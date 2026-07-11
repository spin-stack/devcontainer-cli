package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

func realTemplatesMetadataCmd() *cobra.Command {
	var logLevel string

	cmd := &cobra.Command{
		Use:   "metadata [templateId]",
		Short: "Fetch a published Template's metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTemplatesMetadata(args[0], logLevel)
		},
	}

	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")
	return cmd
}

func runTemplatesMetadata(templateID, logLevel string) error {
	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevel),
		Format: "text",
		Writer: os.Stderr,
	})

	ref, err := oci.ParseRef(templateID)
	if err != nil {
		fmt.Fprintln(os.Stdout, "{}")
		return fmt.Errorf("failed to parse template identifier %q: %w", templateID, err)
	}

	client := oci.NewClient(logger, osEnvMap())

	manifest, err := client.FetchManifest(ref, "")
	if err != nil {
		fmt.Fprintln(os.Stdout, "{}")
		return fmt.Errorf("failed to fetch manifest for template %q: %w", templateID, err)
	}

	logger.Write(fmt.Sprintf("Template %q resolved to %q", templateID, manifest.CanonicalID), log.LevelTrace)

	metadataJSON, ok := manifest.Manifest.Annotations["dev.containers.metadata"]
	if !ok || metadataJSON == "" {
		fmt.Fprintln(os.Stdout, "{}")
		return fmt.Errorf("template resolved to %q but does not contain metadata", manifest.CanonicalID)
	}

	// Parse and re-emit for clean formatting
	var metadata interface{}
	json.Unmarshal([]byte(metadataJSON), &metadata)
	out, _ := json.Marshal(metadata)
	fmt.Fprintln(os.Stdout, string(out))

	return nil
}
