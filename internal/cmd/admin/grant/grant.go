// Package grant provides the admin grant command.
package grant

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/clawker"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// GrantOptions holds options for the grant command.
type GrantOptions struct {
	IOStreams *iostreams.IOStreams
	Session   func() clawker.Session
}

// NewCmdGrant creates the admin grant command.
func NewCmdGrant(f *cmdutil.Factory, runF func(*GrantOptions) error) *cobra.Command {
	opts := &GrantOptions{
		IOStreams: f.IOStreams,
		Session:   f.Session,
	}

	cmd := &cobra.Command{
		Use:   "grant",
		Short: "Grant the host permissions clawker needs",
		Long: `Grants the elevated host permissions clawker needs.

Safe to re-run: permissions already in place are left untouched.`,
		Example: `  # Grant the host permissions clawker needs
  sudo clawker admin grant`,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := opts.Session()
			session.SetNotifications(false)
			session.SetFileLogging(false)
			if runF != nil {
				return runF(opts)
			}
			return grantRun(opts)
		},
	}

	return cmd
}

func grantRun(opts *GrantOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	granted, err := grantBPFFS()
	if err != nil {
		return err
	}
	if !granted {
		fmt.Fprintln(ios.Out, "All permissions already in place.")
		return nil
	}

	fmt.Fprintf(ios.Out, "%s Granted eBPF permissions.\n", cs.SuccessIcon())
	return nil
}
