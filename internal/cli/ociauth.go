package cli

import (
	"context"
	"fmt"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

const (
	flagOCIAuthHardening        = "oci-auth-hardening"
	flagAllowCrossOriginAuthHos = "allow-cross-origin-auth-host"
)

type ociAuthContextKey struct{}

func addOCIAuthFlags(root *cobra.Command) {
	f := root.PersistentFlags()
	f.Bool(flagOCIAuthHardening, false, "Restrict OCI bearer authentication realms, registry credential forwarding, and token redirects.")
	f.StringArray(flagAllowCrossOriginAuthHos, nil, "Allow an OCI registry to use a cross-origin HTTPS authentication host. Format: <registry-host>=<auth-host>. May be repeated.")
}

func buildOCIAuthPolicy(cmd *cobra.Command, logger log.Logger) (*oci.AuthPolicy, error) {
	hardening, _ := cmd.Flags().GetBool(flagOCIAuthHardening)
	hosts, _ := cmd.Flags().GetStringArray(flagAllowCrossOriginAuthHos)
	if len(hosts) > 0 && !hardening {
		return nil, fmt.Errorf("--allow-cross-origin-auth-host requires --oci-auth-hardening.")
	}
	return oci.NewAuthPolicy(hardening, hosts, logger)
}

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

func ociAuthPolicy(ctx context.Context) *oci.AuthPolicy {
	if ctx != nil {
		if policy, ok := ctx.Value(ociAuthContextKey{}).(*oci.AuthPolicy); ok && policy != nil {
			return policy
		}
	}
	return oci.DefaultAuthPolicy()
}

func newOCIClient(ctx context.Context, logger log.Logger) *oci.Client {
	return oci.NewClientWithAuthPolicy(logger, osEnvMap(), ociAuthPolicy(ctx))
}

func ociAuthDiagnostics(ctx context.Context) oci.AuthDiagnostics {
	return ociAuthPolicy(ctx).Diagnostics()
}

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
