package ps

import (
	"github.com/schmitthub/clawker/pkg/cmd/container/list"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdPs creates the ps command as an alias to container list.
// This follows Docker's pattern where `docker ps` is an alias for `docker container list`.
func NewCmdPs(f *cmdutil.Factory) *cobra.Command {
	cmd := list.NewCmdList(f)
	cmd.Use = "ps [OPTIONS]"
	return cmd
}
