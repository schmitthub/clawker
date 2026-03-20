package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	fwpkg "github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RotateCAOptions holds the options for the firewall rotate-ca command.
type RotateCAOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Firewall  func(context.Context) (fwpkg.FirewallManager, error)
}

// NewCmdRotateCA creates the firewall rotate-ca command.
func NewCmdRotateCA(f *cmdutil.Factory, runF func(context.Context, *RotateCAOptions) error) *cobra.Command {
	opts := &RotateCAOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Firewall:  f.Firewall,
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

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get current rules to regenerate domain certs.
	fwMgr, err := opts.Firewall(ctx)
	if err != nil {
		return fmt.Errorf("connecting to firewall: %w", err)
	}

	rules, err := fwMgr.List(ctx)
	if err != nil {
		return fmt.Errorf("listing rules for cert regeneration: %w", err)
	}

	certDir, err := cfg.FirewallCertSubdir()
	if err != nil {
		return fmt.Errorf("resolving firewall cert directory: %w", err)
	}

	if err := fwpkg.RotateCA(certDir, rules); err != nil {
		return fmt.Errorf("rotating CA: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s CA certificate rotated\n", cs.SuccessIcon())
	fmt.Fprintf(ios.ErrOut, "%s Rebuild images and recreate containers for changes to take effect\n",
		cs.WarningIcon())

	return nil
}
