// Package run provides the top-level run command as an alias to container run.
package run

import (
	containerrun "github.com/schmitthub/clawker/pkg/cmd/container/run"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRun creates the run command as an alias to container run.
// This follows Docker's pattern where `docker run` is an alias for `docker container run`.
func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	cmd := containerrun.NewCmd(f)
	cmd.Use = "run [OPTIONS] IMAGE [COMMAND] [ARG...]"
	return cmd
}
