package reset

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/spf13/cobra"
)

// ResetOptions holds options for the loop reset command.
type ResetOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() *config.Config

	Agent    string
	ClearAll bool
	Quiet    bool
}

func NewCmdReset(f *cmdutil.Factory, runF func(context.Context, *ResetOptions) error) *cobra.Command {
	opts := &ResetOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset the circuit breaker for an agent",
		Long: `Reset the circuit breaker to allow loops to continue.

The circuit breaker trips when an agent shows no progress for multiple
consecutive loops. Use this command to reset it and retry.

By default, only the circuit breaker is reset. Use --all to also clear
the session history.`,
		Example: `  # Reset circuit breaker only
  clawker loop reset --agent dev

  # Reset everything (circuit and session)
  clawker loop reset --agent dev --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return resetRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().BoolVar(&opts.ClearAll, "all", false, "Also clear session history")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress output")

	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func resetRun(_ context.Context, opts *ResetOptions) error {
	ios := opts.IOStreams

	// Get config
	cfg := opts.Config().Project

	// Get session store
	store, err := loop.DefaultSessionStore()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to create session store: %v", err)
		return err
	}

	// Reset circuit breaker
	if err := store.DeleteCircuitState(cfg.Project, opts.Agent); err != nil {
		cmdutil.PrintError(ios, "Failed to reset circuit breaker: %v", err)
		return err
	}

	if !opts.Quiet {
		fmt.Fprintf(ios.ErrOut, "Circuit breaker reset for %s.%s\n", cfg.Project, opts.Agent)
	}

	// Optionally clear session
	if opts.ClearAll {
		if err := store.DeleteSession(cfg.Project, opts.Agent); err != nil {
			cmdutil.PrintError(ios, "Failed to clear session: %v", err)
			return err
		}
		if !opts.Quiet {
			fmt.Fprintf(ios.ErrOut, "Session history cleared\n")
		}
	}

	return nil
}
