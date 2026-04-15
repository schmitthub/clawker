package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
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
			return rotateCA(cmd.Context(), opts)
		},
	}

	return cmd
}

func rotateCA(ctx context.Context, opts *RotateCAOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	if _, err := client.FirewallRotateCA(ctx, &adminv1.FirewallRotateCARequest{}); err != nil {
		return fmt.Errorf("rotating CA: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s CA certificate rotated\n", cs.SuccessIcon())
	fmt.Fprintf(ios.Out, "%s Rebuild images and recreate containers for changes to take effect\n",
		cs.WarningIcon())

	return nil
}
