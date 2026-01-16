// Package start provides the top-level start command as an alias to container start.
package start

import (
	"github.com/schmitthub/clawker/pkg/cmd/container/start"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdStart creates the start command as an alias to container start.
// This follows Docker's pattern where `docker start` is an alias for `docker container start`.
func NewCmdStart(f *cmdutil.Factory) *cobra.Command {
	cmd := start.NewCmdStart(f)
	cmd.Use = "start CONTAINER [CONTAINER...]"
	return cmd
}
