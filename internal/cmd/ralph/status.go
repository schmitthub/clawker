package ralph

import (
	"encoding/json"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/ralph"
	"github.com/spf13/cobra"
)

// StatusOptions holds options for the ralph status command.
type StatusOptions struct {
	Agent string
	JSON  bool
}

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	opts := &StatusOptions{}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current ralph session status",
		Long: `Display the current status of a ralph session for an agent.

Shows information about:
  - Session state (started, updated, loops completed)
  - Circuit breaker state (tripped, no-progress count)
  - Cumulative statistics (tasks completed, files modified)`,
		Example: `  # Show status for an agent
  clawker ralph status --agent dev

  # Output as JSON
  clawker ralph status --agent dev --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func runStatus(f *cmdutil.Factory, opts *StatusOptions) error {
	ios := f.IOStreams

	// Load config
	cfg, err := f.Config()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load config: %v", err)
		return err
	}

	// Get session store
	store, err := ralph.DefaultSessionStore()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to create session store: %v", err)
		return err
	}

	// Load session
	session, err := store.LoadSession(cfg.Project, opts.Agent)
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load session: %v", err)
		return err
	}

	// Load circuit state
	circuitState, err := store.LoadCircuitState(cfg.Project, opts.Agent)
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load circuit state: %v", err)
		return err
	}

	// Check if any data exists
	if session == nil && circuitState == nil {
		if opts.JSON {
			fmt.Fprintln(ios.Out, "{\"exists\": false}")
		} else {
			fmt.Fprintf(ios.ErrOut, "No ralph session found for agent %q\n", opts.Agent)
		}
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
		data, jsonErr := json.MarshalIndent(output, "", "  ")
		if jsonErr != nil {
			cmdutil.PrintError(ios, "Failed to encode JSON output: %v", jsonErr)
			return fmt.Errorf("json encoding failed: %w", jsonErr)
		}
		fmt.Fprintln(ios.Out, string(data))
		return nil
	}

	// Human-readable output
	fmt.Fprintf(ios.ErrOut, "Ralph status for %s.%s\n", cfg.Project, opts.Agent)
	fmt.Fprintf(ios.ErrOut, "\n")

	if session != nil {
		fmt.Fprintf(ios.ErrOut, "Session:\n")
		fmt.Fprintf(ios.ErrOut, "  Started: %s\n", session.StartedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(ios.ErrOut, "  Updated: %s\n", session.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(ios.ErrOut, "  Loops completed: %d\n", session.LoopsCompleted)
		fmt.Fprintf(ios.ErrOut, "  Current status: %s\n", session.Status)
		fmt.Fprintf(ios.ErrOut, "  Total tasks: %d\n", session.TotalTasksCompleted)
		fmt.Fprintf(ios.ErrOut, "  Total files: %d\n", session.TotalFilesModified)
		if session.LastError != "" {
			fmt.Fprintf(ios.ErrOut, "  Last error: %s\n", session.LastError)
		}
		fmt.Fprintf(ios.ErrOut, "\n")
	}

	if circuitState != nil {
		fmt.Fprintf(ios.ErrOut, "Circuit breaker:\n")
		if circuitState.Tripped {
			fmt.Fprintf(ios.ErrOut, "  Status: TRIPPED\n")
			fmt.Fprintf(ios.ErrOut, "  Reason: %s\n", circuitState.TripReason)
			if circuitState.TrippedAt != nil {
				fmt.Fprintf(ios.ErrOut, "  Tripped at: %s\n", circuitState.TrippedAt.Format("2006-01-02 15:04:05"))
			}
			fmt.Fprintf(ios.ErrOut, "\n")
			cmdutil.PrintNextSteps(ios,
				fmt.Sprintf("Reset the circuit: clawker ralph reset --agent %s", opts.Agent),
			)
		} else {
			fmt.Fprintf(ios.ErrOut, "  Status: OK\n")
			fmt.Fprintf(ios.ErrOut, "  No-progress count: %d\n", circuitState.NoProgressCount)
		}
	}

	return nil
}
