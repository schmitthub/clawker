package ralph

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/ralph"
	"github.com/spf13/cobra"
)

// ResetOptions holds options for the ralph reset command.
type ResetOptions struct {
	Agent    string
	ClearAll bool
	Quiet    bool
}

func newCmdReset(f *cmdutil.Factory) *cobra.Command {
	opts := &ResetOptions{}

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset the circuit breaker for an agent",
		Long: `Reset the circuit breaker to allow ralph loops to continue.

The circuit breaker trips when an agent shows no progress for multiple
consecutive loops. Use this command to reset it and retry.

By default, only the circuit breaker is reset. Use --all to also clear
the session history.`,
		Example: `  # Reset circuit breaker only
  clawker ralph reset --agent dev

  # Reset everything (circuit and session)
  clawker ralph reset --agent dev --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReset(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().BoolVar(&opts.ClearAll, "all", false, "Also clear session history")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress output")

	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func runReset(f *cmdutil.Factory, opts *ResetOptions) error {
	ios := f.IOStreams

	// Load config
	cfg, err := f.Config()
	if err != nil {
		cmdutil.PrintError("Failed to load config: %v", err)
		return err
	}

	// Get session store
	store, err := ralph.DefaultSessionStore()
	if err != nil {
		cmdutil.PrintError("Failed to create session store: %v", err)
		return err
	}

	// Reset circuit breaker
	if err := store.DeleteCircuitState(cfg.Project, opts.Agent); err != nil {
		cmdutil.PrintError("Failed to reset circuit breaker: %v", err)
		return err
	}

	if !opts.Quiet {
		fmt.Fprintf(ios.ErrOut, "Circuit breaker reset for %s.%s\n", cfg.Project, opts.Agent)
	}

	// Optionally clear session
	if opts.ClearAll {
		if err := store.DeleteSession(cfg.Project, opts.Agent); err != nil {
			cmdutil.PrintError("Failed to clear session: %v", err)
			return err
		}
		if !opts.Quiet {
			fmt.Fprintf(ios.ErrOut, "Session history cleared\n")
		}
	}

	return nil
}
