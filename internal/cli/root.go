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

func NewRootCommand() *cobra.Command {
	cfg := product.Get()

	root := &cobra.Command{
		Use:     cfg.Name,
		Short:   "Dev Container CLI",
		Version: cfg.Version,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.DisableFlagParsing {
				return nil
			}
			return applyOCIAuthPolicy(cmd)
		},
	}

	root.SetVersionTemplate("{{.Version}}\n")

	addOCIAuthFlags(root)

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
		newStopCmd(),
		newDownCmd(),
	)

	return root
}

// mutate-lane: TestParityMatrix (exec.workspace-image-nonzero-exit passes an
// ExitCodeError's code through as 7, exec.container-not-found prints the error
// and exits 1, and every success case exits 0).
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		if exitErr, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
