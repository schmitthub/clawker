package auth

import (
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdAuth creates the auth parent command.
func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage control plane authentication material",
		Long: `Manage the CLI's authentication material used to communicate with the
clawker control plane. The CLI is the root of trust — it generates the CA
certificates, signing keys, and server TLS certificates the control plane uses.`,
	}

	cmd.AddCommand(NewCmdRotate(f, nil))

	return cmd
}
