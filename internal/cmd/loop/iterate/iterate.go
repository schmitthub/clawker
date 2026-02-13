// Package iterate provides the `clawker loop iterate` command.
package iterate

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
)

// IterateOptions holds options for the loop iterate command.
type IterateOptions struct {
	*shared.LoopOptions

	// Factory DI
	IOStreams   *iostreams.IOStreams
	TUI        *tui.TUI
	Client     func(context.Context) (*docker.Client, error)
	Config     func() *config.Config
	GitManager func() (*git.GitManager, error)
	Prompter   func() *prompter.Prompter

	// Prompt source (mutually exclusive, one required)
	Prompt     string
	PromptFile string

	// Output
	Format *cmdutil.FormatFlags
}

// NewCmdIterate creates the `clawker loop iterate` command.
func NewCmdIterate(f *cmdutil.Factory, runF func(context.Context, *IterateOptions) error) *cobra.Command {
	loopOpts := shared.NewLoopOptions()
	opts := &IterateOptions{
		LoopOptions: loopOpts,
		IOStreams:   f.IOStreams,
		TUI:        f.TUI,
		Client:     f.Client,
		Config:     f.Config,
		GitManager: f.GitManager,
		Prompter:   f.Prompter,
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
  clawker loop iterate --prompt "Refactor auth module" --max-loops 100

  # Stream all agent output in real time
  clawker loop iterate --prompt "Add tests" --verbose

  # Run in a git worktree for isolation
  clawker loop iterate --prompt "Refactor auth" --worktree feature/auth

  # Output final result as JSON
  clawker loop iterate --prompt "Fix tests" --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return iterateRun(cmd.Context(), opts)
		},
	}

	// Prompt source flags
	cmd.Flags().StringVarP(&opts.Prompt, "prompt", "p", "", "Prompt to repeat each iteration")
	cmd.Flags().StringVar(&opts.PromptFile, "prompt-file", "", "Path to file containing the prompt")

	// Shared loop flags
	shared.AddLoopFlags(cmd, loopOpts)

	// Output format flags (--json, --quiet, --format)
	opts.Format = cmdutil.AddFormatFlags(cmd)

	// Mutual exclusivity and requirements
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagsOneRequired("prompt", "prompt-file")
	shared.MarkVerboseExclusive(cmd)

	return cmd
}

func iterateRun(_ context.Context, opts *IterateOptions) error {
	cs := opts.IOStreams.ColorScheme()
	fmt.Fprintf(opts.IOStreams.ErrOut, "%s loop iterate is not yet implemented\n", cs.WarningIcon())
	return nil
}
