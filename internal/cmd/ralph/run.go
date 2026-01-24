package ralph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/ralph"
	"github.com/spf13/cobra"
)

// RunOptions holds options for the ralph run command.
type RunOptions struct {
	Agent               string
	Prompt              string
	PromptFile          string
	MaxLoops            int
	StagnationThreshold int
	Timeout             time.Duration
	ResetCircuit        bool
	Quiet               bool
	JSON                bool
}

func newCmdRun(f *cmdutil.Factory) *cobra.Command {
	opts := &RunOptions{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start an autonomous Claude Code loop",
		Long: `Run Claude Code in an autonomous loop until completion or stagnation.

The agent will run Claude Code repeatedly with --continue, parsing each
iteration's output for a RALPH_STATUS block. The loop exits when:

  - Claude signals EXIT_SIGNAL: true or STATUS: COMPLETE
  - The circuit breaker trips (no progress for N consecutive loops)
  - Maximum loops reached
  - An error occurs

The container must already be running. Use 'clawker start' first.`,
		Example: `  # Start with an initial prompt
  clawker ralph run --agent dev --prompt "Fix all failing tests"

  # Start with a prompt from a file
  clawker ralph run --agent dev --prompt-file task.md

  # Continue an existing session
  clawker ralph run --agent dev

  # Reset circuit breaker and retry
  clawker ralph run --agent dev --reset-circuit

  # Run with custom limits
  clawker ralph run --agent dev --max-loops 100 --stagnation-threshold 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRalph(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().StringVarP(&opts.Prompt, "prompt", "p", "", "Initial prompt for the first loop")
	cmd.Flags().StringVar(&opts.PromptFile, "prompt-file", "", "File containing the initial prompt")
	cmd.Flags().IntVar(&opts.MaxLoops, "max-loops", 50, "Maximum number of loops")
	cmd.Flags().IntVar(&opts.StagnationThreshold, "stagnation-threshold", 3, "Loops without progress before circuit trips")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 15*time.Minute, "Timeout per loop iteration")
	cmd.Flags().BoolVar(&opts.ResetCircuit, "reset-circuit", false, "Reset circuit breaker before starting")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress progress output")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output result as JSON")

	_ = cmd.MarkFlagRequired("agent")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")

	return cmd
}

func runRalph(f *cmdutil.Factory, opts *RunOptions) error {
	ctx := context.Background()

	// Load config
	cfg, err := f.Config()
	if err != nil {
		cmdutil.PrintError("Failed to load config: %v", err)
		return err
	}

	// Resolve prompt from file if specified
	prompt := opts.Prompt
	if opts.PromptFile != "" {
		data, err := os.ReadFile(opts.PromptFile)
		if err != nil {
			cmdutil.PrintError("Failed to read prompt file: %v", err)
			return err
		}
		prompt = string(data)
	}

	// Apply config defaults if CLI flags use default values
	if cfg.Ralph != nil {
		if opts.MaxLoops == 50 && cfg.Ralph.MaxLoops > 0 {
			opts.MaxLoops = cfg.Ralph.MaxLoops
		}
		if opts.StagnationThreshold == 3 && cfg.Ralph.StagnationThreshold > 0 {
			opts.StagnationThreshold = cfg.Ralph.StagnationThreshold
		}
		if opts.Timeout == 15*time.Minute && cfg.Ralph.TimeoutMinutes > 0 {
			opts.Timeout = time.Duration(cfg.Ralph.TimeoutMinutes) * time.Minute
		}
	}

	// Build container name
	containerName := docker.ContainerName(cfg.Project, opts.Agent)

	// Get docker client
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Verify container exists and is running
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	if c.State != "running" {
		cmdutil.PrintError("Container %q is not running", containerName)
		cmdutil.PrintNextSteps(
			fmt.Sprintf("Start the container: clawker start --agent %s", opts.Agent),
			"Or create a new one: clawker run --agent "+opts.Agent,
		)
		return fmt.Errorf("container not running")
	}

	// Create runner
	runner, err := ralph.NewRunner(client)
	if err != nil {
		cmdutil.PrintError("Failed to create runner: %v", err)
		return err
	}

	// Set up callbacks for progress output
	var onLoopStart func(int)
	var onLoopEnd func(int, *ralph.Status, error)

	if !opts.Quiet && !opts.JSON {
		onLoopStart = func(loopNum int) {
			fmt.Fprintf(os.Stderr, "Loop %d/%d starting...\n", loopNum, opts.MaxLoops)
		}
		onLoopEnd = func(loopNum int, status *ralph.Status, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "Loop %d: error: %v\n", loopNum, err)
			} else if status != nil {
				fmt.Fprintf(os.Stderr, "Loop %d: %s\n", loopNum, status.String())
			} else {
				fmt.Fprintf(os.Stderr, "Loop %d: no status block found\n", loopNum)
			}
		}
	}

	if !opts.Quiet && !opts.JSON {
		fmt.Fprintf(os.Stderr, "Starting ralph loop for %s...\n", containerName)
	}

	logger.Info().
		Str("container", containerName).
		Str("agent", opts.Agent).
		Int("max_loops", opts.MaxLoops).
		Int("stagnation_threshold", opts.StagnationThreshold).
		Msg("starting ralph loop")

	// Run the loop
	result, err := runner.Run(ctx, ralph.LoopOptions{
		ContainerName:       containerName,
		Project:             cfg.Project,
		Agent:               opts.Agent,
		Prompt:              prompt,
		MaxLoops:            opts.MaxLoops,
		StagnationThreshold: opts.StagnationThreshold,
		Timeout:             opts.Timeout,
		ResetCircuit:        opts.ResetCircuit,
		OnLoopStart:         onLoopStart,
		OnLoopEnd:           onLoopEnd,
	})
	// Ensure result is never nil to avoid nil pointer dereference below
	if result == nil {
		result = &ralph.LoopResult{Error: err}
	}
	if err != nil {
		// Error is already logged by the runner
		if !opts.JSON {
			cmdutil.PrintError("Ralph loop failed: %v", err)
		}
	}

	// Output result
	if opts.JSON {
		output := map[string]any{
			"loops_completed": result.LoopsCompleted,
			"exit_reason":     result.ExitReason,
			"success":         result.Error == nil,
		}
		if result.Error != nil {
			output["error"] = result.Error.Error()
		}
		if result.FinalStatus != nil {
			output["final_status"] = map[string]any{
				"status":          result.FinalStatus.Status,
				"tasks_completed": result.FinalStatus.TasksCompleted,
				"files_modified":  result.FinalStatus.FilesModified,
				"tests_status":    result.FinalStatus.TestsStatus,
				"work_type":       result.FinalStatus.WorkType,
				"recommendation":  result.FinalStatus.Recommendation,
			}
		}
		if result.Session != nil {
			output["session"] = map[string]any{
				"total_tasks_completed": result.Session.TotalTasksCompleted,
				"total_files_modified":  result.Session.TotalFilesModified,
			}
		}
		data, jsonErr := json.MarshalIndent(output, "", "  ")
		if jsonErr != nil {
			cmdutil.PrintError("Failed to encode JSON output: %v", jsonErr)
			return fmt.Errorf("json encoding failed: %w", jsonErr)
		}
		fmt.Fprintln(f.IOStreams.Out, string(data))
		if result.Error != nil {
			return result.Error
		}
		return nil
	}

	// Human-readable output
	if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Ralph loop finished\n")
		fmt.Fprintf(os.Stderr, "  Loops completed: %d\n", result.LoopsCompleted)
		fmt.Fprintf(os.Stderr, "  Exit reason: %s\n", result.ExitReason)
		if result.Session != nil {
			fmt.Fprintf(os.Stderr, "  Total tasks: %d\n", result.Session.TotalTasksCompleted)
			fmt.Fprintf(os.Stderr, "  Total files: %d\n", result.Session.TotalFilesModified)
		}
		if result.FinalStatus != nil && result.FinalStatus.Recommendation != "" {
			fmt.Fprintf(os.Stderr, "  Recommendation: %s\n", result.FinalStatus.Recommendation)
		}
	}

	if result.Error != nil {
		return result.Error
	}
	return nil
}
