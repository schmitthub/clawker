// Package shared provides common flag types and options for loop subcommands.
package shared

import (
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/spf13/cobra"
)

// LoopOptions holds flags shared between loop iterate and loop tasks commands.
// Command-specific Options structs embed this to get the common flags.
type LoopOptions struct {
	// Loop control
	MaxLoops            int
	StagnationThreshold int
	TimeoutMinutes      int
	LoopDelay           int

	// Circuit breaker tuning
	SameErrorThreshold        int
	OutputDeclineThreshold    int
	MaxConsecutiveTestLoops   int
	SafetyCompletionThreshold int
	CompletionThreshold       int
	StrictCompletion          bool

	// Execution
	SkipPermissions bool
	CallsPerHour    int
	ResetCircuit    bool

	// Hooks
	HooksFile string

	// System prompt
	AppendSystemPrompt string

	// Container
	Agent    string
	Worktree string
	Image    string

	// Output
	Verbose bool
}

// NewLoopOptions creates a LoopOptions with zero values.
// Defaults are applied via Cobra flag registration in AddLoopFlags.
func NewLoopOptions() *LoopOptions {
	return &LoopOptions{}
}

// AddLoopFlags registers shared loop flags on the Cobra command.
// Call this before AddFormatFlags to ensure correct PreRunE chain ordering.
func AddLoopFlags(cmd *cobra.Command, opts *LoopOptions) {
	flags := cmd.Flags()

	// Loop control
	flags.IntVar(&opts.MaxLoops, "max-loops", loop.DefaultMaxLoops,
		"Maximum number of iterations")
	flags.IntVar(&opts.StagnationThreshold, "stagnation-threshold", loop.DefaultStagnationThreshold,
		"Iterations without progress before circuit breaker trips")
	flags.IntVar(&opts.TimeoutMinutes, "timeout", loop.DefaultTimeoutMinutes,
		"Per-iteration timeout in minutes")
	flags.IntVar(&opts.LoopDelay, "loop-delay", loop.DefaultLoopDelaySeconds,
		"Seconds to wait between iterations")

	// Circuit breaker tuning
	flags.IntVar(&opts.SameErrorThreshold, "same-error-threshold", loop.DefaultSameErrorThreshold,
		"Consecutive identical errors before circuit breaker trips")
	flags.IntVar(&opts.OutputDeclineThreshold, "output-decline-threshold", loop.DefaultOutputDeclineThreshold,
		"Output size decline percentage before circuit breaker trips")
	flags.IntVar(&opts.MaxConsecutiveTestLoops, "max-test-loops", loop.DefaultMaxConsecutiveTestLoops,
		"Consecutive test-only iterations before circuit breaker trips")
	flags.IntVar(&opts.SafetyCompletionThreshold, "safety-completion-threshold", loop.DefaultSafetyCompletionThreshold,
		"Iterations with completion indicators but no exit signal before trip")
	flags.IntVar(&opts.CompletionThreshold, "completion-threshold", loop.DefaultCompletionThreshold,
		"Completion indicators required for strict completion")
	flags.BoolVar(&opts.StrictCompletion, "strict-completion", false,
		"Require both exit signal and completion indicators for completion")

	// Execution
	flags.BoolVar(&opts.SkipPermissions, "skip-permissions", false,
		"Allow all tools without prompting")
	flags.IntVar(&opts.CallsPerHour, "calls-per-hour", loop.DefaultCallsPerHour,
		"API call rate limit per hour (0 to disable)")
	flags.BoolVar(&opts.ResetCircuit, "reset-circuit", false,
		"Reset circuit breaker before starting")

	// Hooks
	flags.StringVar(&opts.HooksFile, "hooks-file", "",
		"Path to hook configuration file (overrides default hooks)")

	// System prompt
	flags.StringVar(&opts.AppendSystemPrompt, "append-system-prompt", "",
		"Additional system prompt instructions appended to the LOOP_STATUS default")

	// Container
	flags.StringVar(&opts.Agent, "agent", "",
		"Agent name (identifies container and session)")
	flags.StringVar(&opts.Worktree, "worktree", "",
		"Run in a git worktree (optional branch[:base] spec, empty for auto-generated)")
	flags.StringVar(&opts.Image, "image", "",
		"Override container image (default: project config or user settings)")

	// Output
	flags.BoolVarP(&opts.Verbose, "verbose", "v", false,
		"Stream all agent output in real time (non-interactive)")
}

// MarkVerboseExclusive marks --verbose as mutually exclusive with output format flags.
// Must be called after both AddLoopFlags and AddFormatFlags.
func MarkVerboseExclusive(cmd *cobra.Command) {
	cmd.MarkFlagsMutuallyExclusive("verbose", "json")
	cmd.MarkFlagsMutuallyExclusive("verbose", "quiet")
	cmd.MarkFlagsMutuallyExclusive("verbose", "format")
}
