package cli

import "github.com/spf13/cobra"

func newFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "Features commands",
	}
	cmd.AddCommand(
		realFeaturesTestCmd(),
		realFeaturesPackageCmd(),
		realFeaturesPublishCmd(),
		realFeaturesInfoCmd(),
		realFeaturesResolveDepsCmd(),
		realFeaturesGenerateDocsCmd(),
	)
	return cmd
}

func newTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Templates commands",
	}
	cmd.AddCommand(
		realTemplatesApplyCmd(),
		realTemplatesPublishCmd(),
		realTemplatesMetadataCmd(),
		realTemplatesGenerateDocsCmd(),
	)
	return cmd
}
