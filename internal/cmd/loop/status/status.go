package status

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// StatusOptions holds options for the loop status command.
type StatusOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() *config.Config

	Agent string
	JSON  bool
}

func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current loop session status",
		Long: `Display the current status of a loop session for an agent.

Shows information about:
  - Session state (started, updated, loops completed)
  - Circuit breaker state (tripped, no-progress count)
  - Cumulative statistics (tasks completed, files modified)`,
		Example: `  # Show status for an agent
  clawker loop status --agent dev

  # Output as JSON
  clawker loop status --agent dev --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return statusRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func statusRun(_ context.Context, opts *StatusOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Get config
	cfg := opts.Config().Project

	// Get session store
	store, err := shared.DefaultSessionStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}

	// Load session
	session, err := store.LoadSession(cfg.Project, opts.Agent)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}

	// Load circuit state
	circuitState, err := store.LoadCircuitState(cfg.Project, opts.Agent)
	if err != nil {
		return fmt.Errorf("loading circuit state: %w", err)
	}

	// Check if any data exists
	if session == nil && circuitState == nil {
		if opts.JSON {
			return cmdutil.WriteJSON(ios.Out, map[string]any{"exists": false})
		}
		fmt.Fprintf(ios.ErrOut, "No loop session found for agent %q\n", opts.Agent)
		return nil
	}

	if opts.JSON {
		output := map[string]any{
			"exists": true,
		}
		if session != nil {
			output["session"] = map[string]any{
				"project":               session.Project,
				"agent":                 session.Agent,
				"started_at":            session.StartedAt,
				"updated_at":            session.UpdatedAt,
				"loops_completed":       session.LoopsCompleted,
				"status":                session.Status,
				"no_progress_count":     session.NoProgressCount,
				"total_tasks_completed": session.TotalTasksCompleted,
				"total_files_modified":  session.TotalFilesModified,
				"last_error":            session.LastError,
				"initial_prompt":        session.InitialPrompt,
			}
		}
		if circuitState != nil {
			output["circuit"] = map[string]any{
				"tripped":           circuitState.Tripped,
				"trip_reason":       circuitState.TripReason,
				"tripped_at":        circuitState.TrippedAt,
				"no_progress_count": circuitState.NoProgressCount,
			}
		}
		return cmdutil.WriteJSON(ios.Out, output)
	}

	// Human-readable output — primary data goes to stdout
	fmt.Fprintf(ios.Out, "Loop status for %s.%s\n\n", cfg.Project, opts.Agent)

	if session != nil {
		fmt.Fprintf(ios.Out, "Session:\n")
		fmt.Fprintf(ios.Out, "  Started: %s\n", session.StartedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(ios.Out, "  Updated: %s\n", session.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(ios.Out, "  Loops completed: %d\n", session.LoopsCompleted)
		fmt.Fprintf(ios.Out, "  Current status: %s\n", session.Status)
		fmt.Fprintf(ios.Out, "  Total tasks: %d\n", session.TotalTasksCompleted)
		fmt.Fprintf(ios.Out, "  Total files: %d\n", session.TotalFilesModified)
		if session.LastError != "" {
			fmt.Fprintf(ios.Out, "  Last error: %s\n", session.LastError)
		}
		fmt.Fprintf(ios.Out, "\n")
	}

	if circuitState != nil {
		fmt.Fprintf(ios.Out, "Circuit breaker:\n")
		if circuitState.Tripped {
			fmt.Fprintf(ios.Out, "  Status: TRIPPED\n")
			fmt.Fprintf(ios.Out, "  Reason: %s\n", circuitState.TripReason)
			if circuitState.TrippedAt != nil {
				fmt.Fprintf(ios.Out, "  Tripped at: %s\n", circuitState.TrippedAt.Format("2006-01-02 15:04:05"))
			}
			// Next steps guidance goes to stderr
			fmt.Fprintf(ios.ErrOut, "\n%s Reset the circuit: clawker loop reset --agent %s\n",
				cs.InfoIcon(), opts.Agent)
		} else {
			fmt.Fprintf(ios.Out, "  Status: OK\n")
			fmt.Fprintf(ios.Out, "  No-progress count: %d\n", circuitState.NoProgressCount)
		}
	}

	return nil
}
