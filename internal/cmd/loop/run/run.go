package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/spf13/cobra"
)

// RunOptions holds options for the loop run command.
type RunOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent               string
	Prompt              string
	PromptFile          string
	MaxLoops            int
	StagnationThreshold int
	Timeout             time.Duration
	ResetCircuit        bool
	Quiet               bool
	JSON                bool

	CallsPerHour            int
	Monitor                 bool
	Verbose                 bool
	UseStrictCompletion     bool
	SameErrorThreshold      int
	OutputDeclineThreshold  int
	MaxConsecutiveTestLoops int
	LoopDelaySeconds        int
	SkipPermissions         bool
}

func NewCmdRun(f *cmdutil.Factory, runF func(context.Context, *RunOptions) error) *cobra.Command {
	opts := &RunOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start an autonomous Claude Code loop",
		Long: `Run Claude Code in an autonomous loop until completion or stagnation.

The agent will run Claude Code repeatedly with --continue, parsing each
iteration's output for a LOOP_STATUS block. The loop exits when:

  - Claude signals EXIT_SIGNAL: true with sufficient completion indicators
  - The circuit breaker trips (no progress, same error, output decline)
  - Maximum loops reached
  - An error occurs
  - Claude's API rate limit is hit

The container must already be running. Use 'clawker start' first.`,
		Example: `  # Start with an initial prompt
  clawker loop run --agent dev --prompt "Fix all failing tests"

  # Start with a prompt from a file
  clawker loop run --agent dev --prompt-file task.md

  # Continue an existing session
  clawker loop run --agent dev

  # Reset circuit breaker and retry
  clawker loop run --agent dev --reset-circuit

  # Run with custom limits
  clawker loop run --agent dev --max-loops 100 --stagnation-threshold 5

  # Run with live monitoring
  clawker loop run --agent dev --monitor

  # Run with rate limiting (5 calls per hour)
  clawker loop run --agent dev --calls 5

  # Run with verbose output
  clawker loop run --agent dev -v

  # Run in YOLO mode (skip all permission prompts)
  clawker loop run --agent dev --skip-permissions`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return runRun(cmd.Context(), opts)
		},
	}

	// Existing flags
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required)")
	cmd.Flags().StringVarP(&opts.Prompt, "prompt", "p", "", "Initial prompt for the first loop")
	cmd.Flags().StringVar(&opts.PromptFile, "prompt-file", "", "File containing the initial prompt")
	cmd.Flags().IntVar(&opts.MaxLoops, "max-loops", loop.DefaultMaxLoops, "Maximum number of loops")
	cmd.Flags().IntVar(&opts.StagnationThreshold, "stagnation-threshold", loop.DefaultStagnationThreshold, "Loops without progress before circuit trips")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", time.Duration(loop.DefaultTimeoutMinutes)*time.Minute, "Timeout per loop iteration")
	cmd.Flags().BoolVar(&opts.ResetCircuit, "reset-circuit", false, "Reset circuit breaker before starting")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress progress output")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output result as JSON")

	// New flags
	cmd.Flags().IntVar(&opts.CallsPerHour, "calls", loop.DefaultCallsPerHour, "Rate limit: max calls per hour (0 to disable)")
	cmd.Flags().BoolVar(&opts.Monitor, "monitor", false, "Enable live monitoring output")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Enable verbose output")
	cmd.Flags().BoolVar(&opts.UseStrictCompletion, "strict-completion", false, "Require both EXIT_SIGNAL and completion indicators")
	cmd.Flags().IntVar(&opts.SameErrorThreshold, "same-error-threshold", loop.DefaultSameErrorThreshold, "Same error repetitions before circuit trips")
	cmd.Flags().IntVar(&opts.OutputDeclineThreshold, "output-decline-threshold", loop.DefaultOutputDeclineThreshold, "Output decline percentage that triggers trip")
	cmd.Flags().IntVar(&opts.MaxConsecutiveTestLoops, "max-test-loops", loop.DefaultMaxConsecutiveTestLoops, "Consecutive test-only loops before circuit trips")
	cmd.Flags().IntVar(&opts.LoopDelaySeconds, "loop-delay", loop.DefaultLoopDelaySeconds, "Seconds to wait between loop iterations")
	cmd.Flags().BoolVar(&opts.SkipPermissions, "skip-permissions", false, "Pass --dangerously-skip-permissions to claude")

	_ = cmd.MarkFlagRequired("agent")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("quiet", "monitor")
	cmd.MarkFlagsMutuallyExclusive("quiet", "verbose")
	cmd.MarkFlagsMutuallyExclusive("json", "monitor")

	return cmd
}

func runRun(ctx context.Context, opts *RunOptions) error {
	ios := opts.IOStreams

	// Get config
	cfg := opts.Config().Project

	// Resolve prompt from file if specified
	prompt := opts.Prompt
	if opts.PromptFile != "" {
		data, err := os.ReadFile(opts.PromptFile)
		if err != nil {
			cmdutil.PrintError(ios, "Failed to read prompt file: %v", err)
			return err
		}
		prompt = string(data)
	}

	// Apply config defaults if CLI flags use default values
	if cfg.Loop != nil {
		if opts.MaxLoops == loop.DefaultMaxLoops && cfg.Loop.MaxLoops > 0 {
			opts.MaxLoops = cfg.Loop.MaxLoops
		}
		if opts.StagnationThreshold == loop.DefaultStagnationThreshold && cfg.Loop.StagnationThreshold > 0 {
			opts.StagnationThreshold = cfg.Loop.StagnationThreshold
		}
		if opts.Timeout == time.Duration(loop.DefaultTimeoutMinutes)*time.Minute && cfg.Loop.TimeoutMinutes > 0 {
			opts.Timeout = time.Duration(cfg.Loop.TimeoutMinutes) * time.Minute
		}
		if opts.CallsPerHour == loop.DefaultCallsPerHour && cfg.Loop.CallsPerHour > 0 {
			opts.CallsPerHour = cfg.Loop.CallsPerHour
		}
		if opts.SameErrorThreshold == loop.DefaultSameErrorThreshold && cfg.Loop.SameErrorThreshold > 0 {
			opts.SameErrorThreshold = cfg.Loop.SameErrorThreshold
		}
		if opts.OutputDeclineThreshold == loop.DefaultOutputDeclineThreshold && cfg.Loop.OutputDeclineThreshold > 0 {
			opts.OutputDeclineThreshold = cfg.Loop.OutputDeclineThreshold
		}
		if opts.MaxConsecutiveTestLoops == loop.DefaultMaxConsecutiveTestLoops && cfg.Loop.MaxConsecutiveTestLoops > 0 {
			opts.MaxConsecutiveTestLoops = cfg.Loop.MaxConsecutiveTestLoops
		}
		if opts.LoopDelaySeconds == loop.DefaultLoopDelaySeconds && cfg.Loop.LoopDelaySeconds > 0 {
			opts.LoopDelaySeconds = cfg.Loop.LoopDelaySeconds
		}
		// Boolean flags: config overrides false (default) only
		if !opts.SkipPermissions && cfg.Loop.SkipPermissions {
			opts.SkipPermissions = true
		}
	}

	// Build container name
	containerName, err := docker.ContainerName(cfg.Project, opts.Agent)
	if err != nil {
		return err
	}

	// Get docker client
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Verify container exists and is running
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}
	if c.State != "running" {
		cmdutil.PrintError(ios, "Container %q is not running", containerName)
		cmdutil.PrintNextSteps(ios,
			fmt.Sprintf("Start the container: clawker start --agent %s", opts.Agent),
			"Or create a new one: clawker run --agent "+opts.Agent,
		)
		return fmt.Errorf("container not running")
	}

	// Create runner
	runner, err := loop.NewRunner(client)
	if err != nil {
		cmdutil.PrintError(ios, "Failed to create runner: %v", err)
		return err
	}

	// Set up callbacks for progress output
	var onLoopStart func(int)
	var onLoopEnd func(int, *loop.Status, error)
	var monitor *loop.Monitor

	if opts.Monitor && !opts.Quiet && !opts.JSON {
		monitor = loop.NewMonitor(loop.MonitorOptions{
			Writer:        ios.ErrOut,
			MaxLoops:      opts.MaxLoops,
			ShowRateLimit: opts.CallsPerHour > 0,
			Verbose:       opts.Verbose,
		})
	} else if !opts.Quiet && !opts.JSON {
		onLoopStart = func(loopNum int) {
			fmt.Fprintf(ios.ErrOut, "Loop %d/%d starting...\n", loopNum, opts.MaxLoops)
		}
		onLoopEnd = func(loopNum int, status *loop.Status, err error) {
			if err != nil {
				fmt.Fprintf(ios.ErrOut, "Loop %d: error: %v\n", loopNum, err)
			} else if status != nil {
				fmt.Fprintf(ios.ErrOut, "Loop %d: %s\n", loopNum, status.String())
			} else {
				fmt.Fprintf(ios.ErrOut, "Loop %d: no status block found\n", loopNum)
			}
		}
	}

	// Rate limit callback - loop is autonomous, so we exit cleanly instead of prompting
	// This avoids goroutine leaks from blocking stdin reads
	var onRateLimitHit func() bool
	if ios.IsInputTTY() && !opts.Quiet {
		onRateLimitHit = func() bool {
			fmt.Fprintln(ios.ErrOut, "\nClaude's API rate limit hit (5-hour limit).")
			fmt.Fprintln(ios.ErrOut, "Exiting. Retry in ~60 minutes or use --reset-circuit to restart.")
			return false // Always exit, no blocking goroutine
		}
	}

	if !opts.Quiet && !opts.JSON {
		fmt.Fprintf(ios.ErrOut, "Starting loop for %s...\n", containerName)
	}

	logger.Info().
		Str("container", containerName).
		Str("agent", opts.Agent).
		Int("max_loops", opts.MaxLoops).
		Int("stagnation_threshold", opts.StagnationThreshold).
		Int("calls_per_hour", opts.CallsPerHour).
		Msg("starting loop")

	// Run the loop
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:           containerName,
		Project:                 cfg.Project,
		Agent:                   opts.Agent,
		Prompt:                  prompt,
		MaxLoops:                opts.MaxLoops,
		StagnationThreshold:     opts.StagnationThreshold,
		Timeout:                 opts.Timeout,
		ResetCircuit:            opts.ResetCircuit,
		CallsPerHour:            opts.CallsPerHour,
		SameErrorThreshold:      opts.SameErrorThreshold,
		OutputDeclineThreshold:  opts.OutputDeclineThreshold,
		MaxConsecutiveTestLoops: opts.MaxConsecutiveTestLoops,
		LoopDelaySeconds:        opts.LoopDelaySeconds,
		UseStrictCompletion:     opts.UseStrictCompletion,
		SkipPermissions:         opts.SkipPermissions,
		Monitor:                 monitor,
		Verbose:                 opts.Verbose,
		OnLoopStart:             onLoopStart,
		OnLoopEnd:               onLoopEnd,
		OnRateLimitHit:          onRateLimitHit,
	})
	// Ensure result is never nil to avoid nil pointer dereference below
	if result == nil {
		result = &loop.Result{Error: err}
	}
	if err != nil {
		// Error is already logged by the runner
		if !opts.JSON {
			cmdutil.PrintError(ios, "Loop failed: %v", err)
		}
	}

	// Output result
	if opts.JSON {
		output := map[string]any{
			"loops_completed": result.LoopsCompleted,
			"exit_reason":     result.ExitReason,
			"success":         result.Error == nil,
			"rate_limit_hit":  result.RateLimitHit,
		}
		if result.Error != nil {
			output["error"] = result.Error.Error()
		}
		if result.FinalStatus != nil {
			output["final_status"] = map[string]any{
				"status":                result.FinalStatus.Status,
				"tasks_completed":       result.FinalStatus.TasksCompleted,
				"files_modified":        result.FinalStatus.FilesModified,
				"tests_status":          result.FinalStatus.TestsStatus,
				"work_type":             result.FinalStatus.WorkType,
				"recommendation":        result.FinalStatus.Recommendation,
				"completion_indicators": result.FinalStatus.CompletionIndicators,
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
			cmdutil.PrintError(ios, "Failed to encode JSON output: %v", jsonErr)
			return fmt.Errorf("json encoding failed: %w", jsonErr)
		}
		fmt.Fprintln(ios.Out, string(data))
		if result.Error != nil {
			return result.Error
		}
		return nil
	}

	// Human-readable output (skip if monitor already printed)
	if !opts.Quiet && monitor == nil {
		fmt.Fprintf(ios.ErrOut, "\n")
		fmt.Fprintf(ios.ErrOut, "Loop finished\n")
		fmt.Fprintf(ios.ErrOut, "  Loops completed: %d\n", result.LoopsCompleted)
		fmt.Fprintf(ios.ErrOut, "  Exit reason: %s\n", result.ExitReason)
		if result.Session != nil {
			fmt.Fprintf(ios.ErrOut, "  Total tasks: %d\n", result.Session.TotalTasksCompleted)
			fmt.Fprintf(ios.ErrOut, "  Total files: %d\n", result.Session.TotalFilesModified)
		}
		if result.FinalStatus != nil && result.FinalStatus.Recommendation != "" {
			fmt.Fprintf(ios.ErrOut, "  Recommendation: %s\n", result.FinalStatus.Recommendation)
		}
		if result.RateLimitHit {
			fmt.Fprintf(ios.ErrOut, "  Note: Claude API rate limit was hit\n")
		}
	}

	if result.Error != nil {
		return result.Error
	}
	return nil
}
