package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/devcontainers/cli/internal/doctor"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/product"
	"github.com/spf13/cobra"
)

type checkOpts struct {
	json       bool
	dockerPath string
}

// newCheckCmd builds `devcontainer check`: it diagnoses the host's Docker /
// BuildKit environment, prints a ✔/⚠/✖ table with remediation hints, persists
// the result to the state file, and exits non-zero when a hard check fails.
func newCheckCmd() *cobra.Command {
	var opts checkOpts
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Diagnose whether the host is configured to run dev containers",
		Long: "Check probes the local Docker/BuildKit environment (daemon, buildx, build-cache " +
			"export, Compose v2, free disk) and reports what works and what needs attention. " +
			"The result is saved so `up`/`build` can warn about a misconfigured host.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), outputFor(cmd), &opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.json, "json", false, "Output the report as JSON.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	return cmd
}

func runCheck(ctx context.Context, out Output, opts *checkOpts) error {
	env := &doctor.Env{DockerPath: opts.dockerPath, CLIVersion: product.Get().Version}
	report := doctor.Run(ctx, env)

	// Persist best-effort: a diagnostics run must not fail because state could
	// not be written (read-only HOME, etc.). Surface it as a note in text mode.
	saveErr := doctor.Save(report)

	if opts.json {
		if err := writeJSON(out.Stdout(), report); err != nil {
			return err
		}
	} else {
		writeReportTable(out.Stdout(), report)
		if saveErr != nil {
			fmt.Fprintf(out.Stderr(), "note: could not save check state: %v\n", saveErr)
		}
	}

	if report.Overall == doctor.StatusFail {
		return &coreerrors.ExitCodeError{Code: 1}
	}
	return nil
}

type setupOpts struct {
	json       bool
	dryRun     bool
	dockerPath string
}

// newSetupCmd builds `devcontainer setup`: it runs the same diagnostics and then
// applies the automatic remediations (creating a cache-capable buildx builder),
// reporting manual steps for anything it cannot fix without sudo.
//
// Note: this is distinct from `set-up` (hyphenated), which sets up an existing
// container as a dev container.
func newSetupCmd() *cobra.Command {
	var opts setupOpts
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure the host system to run dev containers",
		Long: "Setup runs the same diagnostics as `check` and applies the fixes it safely can " +
			"(e.g. creating a docker-container buildx builder so build-cache export works). " +
			"Fixes that need a package manager or sudo are reported as manual steps.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.Context(), outputFor(cmd), &opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON.")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Report what would be changed without changing anything.")
	f.StringVar(&opts.dockerPath, "docker-path", "", "Docker CLI path.")
	return cmd
}

func runSetup(ctx context.Context, out Output, opts *setupOpts) error {
	env := &doctor.Env{DockerPath: opts.dockerPath, CLIVersion: product.Get().Version}
	report := doctor.Run(ctx, env)
	actions := doctor.Setup(ctx, env, report, opts.dryRun)

	// Re-run diagnostics after remediation so the persisted state reflects the
	// fixed system (skip when nothing was applied or in dry-run).
	final := report
	if !opts.dryRun && appliedAny(actions) {
		final = doctor.Run(ctx, env)
	}
	saveErr := doctor.Save(final)

	if opts.json {
		payload := struct {
			DryRun  bool            `json:"dry_run"`
			Actions []doctor.Action `json:"actions"`
			Report  doctor.Report   `json:"report"`
		}{opts.dryRun, actions, final}
		if err := writeJSON(out.Stdout(), payload); err != nil {
			return err
		}
	} else {
		writeSetupActions(out.Stdout(), actions, opts.dryRun)
		fmt.Fprintln(out.Stdout())
		writeReportTable(out.Stdout(), final)
		if saveErr != nil {
			fmt.Fprintf(out.Stderr(), "note: could not save check state: %v\n", saveErr)
		}
	}

	if !doctor.SetupSucceeded(actions) {
		return &coreerrors.ExitCodeError{Code: 1}
	}
	return nil
}

func appliedAny(actions []doctor.Action) bool {
	for _, a := range actions {
		if a.Applied {
			return true
		}
	}
	return false
}

// writeReportTable renders the ✔/⚠/✖ table plus per-line remediation hints.
func writeReportTable(w io.Writer, report doctor.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range report.Results {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Status.Symbol(), r.Name, r.Summary)
	}
	_ = tw.Flush()
	for _, r := range report.Results {
		if r.Remediation != "" {
			fmt.Fprintf(w, "  → %s: %s\n", r.Name, r.Remediation)
		}
	}
	fmt.Fprintf(w, "\noverall: %s %s\n", report.Overall.Symbol(), report.Overall)
}

// writeSetupActions renders the remediation steps taken (or planned in dry-run).
func writeSetupActions(w io.Writer, actions []doctor.Action, dryRun bool) {
	if len(actions) == 0 {
		fmt.Fprintln(w, "Nothing to do — the host is already configured.")
		return
	}
	header := "Applied:"
	if dryRun {
		header = "Would apply (dry-run):"
	}
	fmt.Fprintln(w, header)
	for _, a := range actions {
		switch {
		case a.Err != "":
			fmt.Fprintf(w, "  %s %s: %s (%s)\n", doctor.StatusFail.Symbol(), a.Name, a.Message, a.Err)
		case a.Applied:
			fmt.Fprintf(w, "  %s %s: %s\n", doctor.StatusOK.Symbol(), a.Name, a.Message)
		default:
			fmt.Fprintf(w, "  %s %s: %s\n", doctor.StatusWarn.Symbol(), a.Name, a.Message)
		}
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// warnUncheckedHost emits a one-line, non-blocking hint on interactive stderr
// when the host was never checked, or a previous `check` found a hard failure.
// It is gated on a TTY so it never perturbs captured output (parity harness,
// scripts) — only humans at a terminal see it.
func warnUncheckedHost(out Output) {
	if !isTerminalWriter(out.Stderr()) {
		return
	}
	report, ok, err := doctor.Load()
	if err != nil {
		return
	}
	if !ok {
		fmt.Fprintln(out.Stderr(), "hint: host not yet checked — run `devcontainer check` to verify your Docker setup.")
		return
	}
	if report.Overall == doctor.StatusFail {
		fmt.Fprintln(out.Stderr(), "warning: `devcontainer check` reported a failing host configuration — run `devcontainer check` for details.")
	}
}

// isTerminalWriter reports whether w is a character device (a real terminal).
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
