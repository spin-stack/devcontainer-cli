package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/product"
	"github.com/spf13/cobra"
)

// NewRootCommand creates the root `devcontainer` command with all subcommands.
func NewRootCommand() *cobra.Command {
	cfg := product.Get()

	root := &cobra.Command{
		Use:     cfg.Name,
		Short:   "Dev Container CLI",
		Version: cfg.Version,
		// Disable default completion command
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		// Match yargs: boolean-negation disabled, strict mode
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Print just the bare version (e.g. "0.74.0"), matching the TS CLI (yargs
	// .version()) instead of Cobra's "<name> version <v>" template.
	root.SetVersionTemplate("{{.Version}}\n")

	// Register subcommands
	root.AddCommand(
		newReadConfigurationCmd(),
		newBuildCmd(),
		newUpCmd(),
		newSetUpCmd(),
		newRunUserCommandsCmd(),
		newExecCmd(),
		newOutdatedCmd(),
		newUpgradeCmd(),
		newFeaturesCmd(),
		newTemplatesCmd(),
		newCheckCmd(),
		newSetupCmd(),
		newOpenCmd(),
	)

	return root
}

// Execute runs the CLI. The command tree is driven by a context that is
// cancelled on SIGINT/SIGTERM so an in-flight `up`/`build` can unwind cleanly
// on Ctrl-C instead of being killed mid-operation.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		var exitErr *coreerrors.ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
