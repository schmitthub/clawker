// Package rotateca provides the firewall rotate-ca command.
package rotateca

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RotateCAOptions holds the options for the firewall rotate-ca command.
type RotateCAOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdRotateCA creates the firewall rotate-ca command.
func NewCmdRotateCA(f *cmdutil.Factory, runF func(context.Context, *RotateCAOptions) error) *cobra.Command {
	opts := &RotateCAOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "rotate-ca",
		Short: "Rotate the firewall CA certificate",
		Long: `Regenerate the CA keypair and all domain certificates used for TLS
inspection. Running containers will need to be rebuilt and recreated
to pick up the new CA.`,
		Example: `  # Rotate the CA certificate
  clawker firewall rotate-ca`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return rotateCARun(cmd.Context(), opts)
		},
	}

	return cmd
}

func rotateCARun(ctx context.Context, opts *RotateCAOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := shared.CallWithSpinner(ctx, ios, "Rotating firewall CA...",
		func(rpcCtx context.Context) (*adminv1.FirewallRotateCAResult, error) {
			return client.FirewallRotateCA(rpcCtx, &adminv1.FirewallRotateCARequest{})
		})
	if err != nil {
		//nolint:wrapcheck // WrapRPCError IS the wrap: it adds the header plus remediation hints
		return shared.WrapRPCError("rotating CA", err)
	}

	fmt.Fprintf(ios.Out, "%s CA certificate rotated\n", cs.SuccessIcon())
	fmt.Fprintf(ios.Out, "%s Rebuild images and recreate containers for changes to take effect\n",
		cs.WarningIcon())
	shared.PrintStackRestartedNote(ios, resp.GetStackRestarted(), "CA + per-domain certs regenerated on disk")

	return nil
}
