package auth

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RotateOptions holds the options for the auth rotate command.
type RotateOptions struct {
	IOStreams *iostreams.IOStreams
	Force     bool
}

// NewCmdRotate creates the auth rotate command.
func NewCmdRotate(f *cmdutil.Factory, runF func(context.Context, *RotateOptions) error) *cobra.Command {
	opts := &RotateOptions{
		IOStreams: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate control plane auth material",
		Long: `Check and rotate authentication material for the control plane.

Without --force, checks all auth files and creates any that are missing.
Existing valid material is not modified (idempotent).

With --force, regenerates the CA certificate, server certificate, and
signing key. The CP must be restarted to pick up new material.

Auth material:
  - CA certificate and key (signs server and client certs, 5-year validity)
  - CLI signing key and JWK (ES256 for OAuth2 private_key_jwt auth)
  - Server TLS certificate and key (signed by CLI CA, 1-year validity)
  - Client mTLS certificate and key (signed by CLI CA, 1-year validity)

Private keys are always created with 0600 permissions.`,
		Example: `  # Check auth material and create any missing files
  clawker auth rotate

  # Force-regenerate all auth material
  clawker auth rotate --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return rotate(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Force, "force", false, "Regenerate all auth material even if valid")

	return cmd
}

func rotate(_ context.Context, opts *RotateOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	if opts.Force {
		fmt.Fprintf(ios.ErrOut, "%s Rotating all auth material...\n", cs.InfoIcon())

		if err := auth.RotateAuthMaterial(true); err != nil {
			return fmt.Errorf("rotating auth material: %w", err)
		}

		fmt.Fprintf(ios.Out, "%s Auth material rotated\n", cs.SuccessIcon())
		fmt.Fprintf(ios.Out, "%s Restart the CP for changes to take effect\n", cs.WarningIcon())
		return nil
	}

	// Without --force: check status, create missing, report.
	statusBefore, err := auth.CheckAuthMaterial()
	if err != nil {
		return fmt.Errorf("checking auth material: %w", err)
	}

	missing := false
	expired := false
	for _, s := range statusBefore {
		if !s.Exists {
			missing = true
		}
		if s.Expired {
			expired = true
		}
	}

	if missing {
		fmt.Fprintf(ios.ErrOut, "%s Creating missing auth material...\n", cs.InfoIcon())
		if err := auth.EnsureAuthMaterial(); err != nil {
			return fmt.Errorf("creating auth material: %w", err)
		}
	}

	// Re-check after potential creation.
	status, err := auth.CheckAuthMaterial()
	if err != nil {
		return fmt.Errorf("checking auth material: %w", err)
	}

	for _, s := range status {
		icon := cs.SuccessIcon()
		detail := ""
		if !s.Exists {
			icon = cs.FailureIcon()
			detail = " (missing)"
		} else if s.Expired {
			icon = cs.WarningIcon()
			detail = fmt.Sprintf(" (expired %s)", s.Expires.Format("2006-01-02"))
		} else if !s.Expires.IsZero() {
			detail = fmt.Sprintf(" (expires %s)", s.Expires.Format("2006-01-02"))
		}
		fmt.Fprintf(ios.Out, "%s %s%s\n", icon, s.Name, detail)
	}

	if expired {
		fmt.Fprintf(ios.Out, "\n%s Expired certificates found. Run with --force to regenerate.\n", cs.WarningIcon())
	}

	return nil
}
