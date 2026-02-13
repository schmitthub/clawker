// Package tasks provides the `clawker loop tasks` command.
package tasks

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

// TasksOptions holds options for the loop tasks command.
type TasksOptions struct {
	*shared.LoopOptions

	// Factory DI
	IOStreams   *iostreams.IOStreams
	TUI        *tui.TUI
	Client     func(context.Context) (*docker.Client, error)
	Config     func() *config.Config
	GitManager func() (*git.GitManager, error)
	Prompter   func() *prompter.Prompter

	// Task file (required)
	TasksFile string

	// Task prompt (mutually exclusive, optional — defaults to built-in template)
	TaskPrompt     string
	TaskPromptFile string

	// Output
	Format *cmdutil.FormatFlags
}

// NewCmdTasks creates the `clawker loop tasks` command.
func NewCmdTasks(f *cmdutil.Factory, runF func(context.Context, *TasksOptions) error) *cobra.Command {
	loopOpts := shared.NewLoopOptions()
	opts := &TasksOptions{
		LoopOptions: loopOpts,
		IOStreams:   f.IOStreams,
		TUI:        f.TUI,
		Client:     f.Client,
		Config:     f.Config,
		GitManager: f.GitManager,
		Prompter:   f.Prompter,
	}

	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Run an agent loop driven by a task file",
		Long: `Run Claude Code in an autonomous loop driven by a task file.

Each iteration, the agent reads the task file, picks an open task, completes
it, and marks it done. Clawker manages the loop — the agent LLM handles task
selection and completion.

The loop exits when:
  - All tasks are completed (agent signals via LOOP_STATUS)
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

Container lifecycle is managed automatically: a container is created at the
start and destroyed on completion.`,
		Example: `  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Run with a custom task prompt template
  clawker loop tasks --tasks todo.md --task-prompt-file instructions.md

  # Run with a custom inline task prompt
  clawker loop tasks --tasks backlog.md --task-prompt "Pick the highest priority task"

  # Stream all agent output in real time
  clawker loop tasks --tasks todo.md --verbose

  # Output final result as JSON
  clawker loop tasks --tasks todo.md --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return tasksRun(cmd.Context(), opts)
		},
	}

	// Task file flag (required)
	cmd.Flags().StringVar(&opts.TasksFile, "tasks", "", "Path to the task file")

	// Task prompt flags (optional, mutually exclusive)
	cmd.Flags().StringVar(&opts.TaskPrompt, "task-prompt", "",
		"Prompt template for task selection and execution")
	cmd.Flags().StringVar(&opts.TaskPromptFile, "task-prompt-file", "",
		"Path to file containing the task prompt template")

	// Shared loop flags
	shared.AddLoopFlags(cmd, loopOpts)

	// Output format flags (--json, --quiet, --format)
	opts.Format = cmdutil.AddFormatFlags(cmd)

	// Requirements and mutual exclusivity
	_ = cmd.MarkFlagRequired("tasks")
	cmd.MarkFlagsMutuallyExclusive("task-prompt", "task-prompt-file")
	shared.MarkVerboseExclusive(cmd)

	return cmd
}

func tasksRun(_ context.Context, opts *TasksOptions) error {
	cs := opts.IOStreams.ColorScheme()
	fmt.Fprintf(opts.IOStreams.ErrOut, "%s loop tasks is not yet implemented\n", cs.WarningIcon())
	return nil
}
