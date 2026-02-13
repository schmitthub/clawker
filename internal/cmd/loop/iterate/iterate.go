// Package iterate provides the `clawker loop iterate` command.
package iterate

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// IterateOptions holds options for the loop iterate command.
type IterateOptions struct {
	IOStreams *iostreams.IOStreams
	Client   func(context.Context) (*docker.Client, error)
	Config   func() *config.Config
}

// NewCmdIterate creates the `clawker loop iterate` command.
func NewCmdIterate(f *cmdutil.Factory, runF func(context.Context, *IterateOptions) error) *cobra.Command {
	opts := &IterateOptions{
		IOStreams: f.IOStreams,
		Client:   f.Client,
		Config:   f.Config,
	}

	cmd := &cobra.Command{
		Use:   "iterate",
		Short: "Run an agent loop with a repeated prompt",
		Long: `Run Claude Code in an autonomous loop, repeating the same prompt each iteration.

Each iteration starts a fresh Claude session (no conversation context carried
forward). The agent only sees the current codebase state from previous runs.

The loop exits when:
  - Claude signals completion via a LOOP_STATUS block
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

Container lifecycle is managed automatically: a container is created at the
start and destroyed on completion.`,
		Example: `  # Run a loop with a prompt
  clawker loop iterate --prompt "Fix all failing tests"

  # Run with a prompt from a file
  clawker loop iterate --prompt-file task.md

  # Run with custom loop limits
  clawker loop iterate --prompt "Refactor auth module" --max-loops 100`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return iterateRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func iterateRun(_ context.Context, opts *IterateOptions) error {
	cs := opts.IOStreams.ColorScheme()
	fmt.Fprintf(opts.IOStreams.ErrOut, "%s loop iterate is not yet implemented\n", cs.WarningIcon())
	return nil
}
