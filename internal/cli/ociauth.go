package cli

import (
	"context"
	"fmt"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

// Global OCI authentication flags, declared on the root command so every
// subcommand accepts them (yargs `.option(..., { global: true })`).
const (
	flagOCIAuthHardening        = "oci-auth-hardening"
	flagAllowCrossOriginAuthHos = "allow-cross-origin-auth-host"
)

// ociAuthContextKey keys the per-invocation OCI auth policy in the command context.
type ociAuthContextKey struct{}

// addOCIAuthFlags declares the global OCI auth flags on the root command.
func addOCIAuthFlags(root *cobra.Command) {
	f := root.PersistentFlags()
	f.Bool(flagOCIAuthHardening, false, "Restrict OCI bearer authentication realms, registry credential forwarding, and token redirects.")
	f.StringArray(flagAllowCrossOriginAuthHos, nil, "Allow an OCI registry to use a cross-origin HTTPS authentication host. Format: <registry-host>=<auth-host>. May be repeated.")
}

// buildOCIAuthPolicy validates the global OCI auth flags and builds the policy
// for this invocation. It mirrors the reference CLI's yargs `.check()`:
// --allow-cross-origin-auth-host requires --oci-auth-hardening, and every entry
// must be a '<registry-host>=<auth-host>' pair of bare authorities.
func buildOCIAuthPolicy(cmd *cobra.Command, logger log.Logger) (*oci.AuthPolicy, error) {
	hardening, _ := cmd.Flags().GetBool(flagOCIAuthHardening)
	hosts, _ := cmd.Flags().GetStringArray(flagAllowCrossOriginAuthHos)
	if len(hosts) > 0 && !hardening {
		return nil, fmt.Errorf("--allow-cross-origin-auth-host requires --oci-auth-hardening.")
	}
	return oci.NewAuthPolicy(hardening, hosts, logger)
}

// applyOCIAuthPolicy validates the global flags and stores the resulting policy on
// the command's context, where the command's OCI clients pick it up.
func applyOCIAuthPolicy(cmd *cobra.Command) error {
	policy, err := buildOCIAuthPolicy(cmd, log.Null)
	if err != nil {
		return err
	}
	cmd.SetContext(withOCIAuthPolicy(cmd.Context(), policy))
	return nil
}

func withOCIAuthPolicy(ctx context.Context, policy *oci.AuthPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ociAuthContextKey{}, policy)
}

// ociAuthPolicy returns the policy stored on ctx, or a default (hardening off)
// policy when a caller runs outside the command tree, e.g. a unit test.
func ociAuthPolicy(ctx context.Context) *oci.AuthPolicy {
	if ctx != nil {
		if policy, ok := ctx.Value(ociAuthContextKey{}).(*oci.AuthPolicy); ok && policy != nil {
			return policy
		}
	}
	return oci.DefaultAuthPolicy()
}

// newOCIClient builds an OCI client bound to this invocation's auth policy, so
// every registry request of the command honors the hardening flags and feeds the
// same diagnostics.
func newOCIClient(ctx context.Context, logger log.Logger) *oci.Client {
	return oci.NewClientWithAuthPolicy(logger, osEnvMap(), ociAuthPolicy(ctx))
}

// ociAuthDiagnostics returns the diagnostics recorded for this invocation, for
// the `ociAuthDiagnostics` field of the command's JSON output.
func ociAuthDiagnostics(ctx context.Context) oci.AuthDiagnostics {
	return ociAuthPolicy(ctx).Diagnostics()
}

// ociAuthGlobalArgs re-renders the global OCI auth flags as CLI arguments, so a
// command that re-invokes this binary (e.g. `features test` spawning `up` for each
// test project) passes the same policy on to the child process.
func ociAuthGlobalArgs(cmd *cobra.Command) []string {
	var args []string
	if hardening, _ := cmd.Flags().GetBool(flagOCIAuthHardening); hardening {
		args = append(args, "--"+flagOCIAuthHardening)
	}
	hosts, _ := cmd.Flags().GetStringArray(flagAllowCrossOriginAuthHos)
	for _, h := range hosts {
		args = append(args, "--"+flagAllowCrossOriginAuthHos, h)
	}
	return args
}
