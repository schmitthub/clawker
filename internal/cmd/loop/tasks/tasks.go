// Package tasks provides the `clawker loop tasks` command.
package tasks

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// TasksOptions holds options for the loop tasks command.
type TasksOptions struct {
	IOStreams *iostreams.IOStreams
	Client   func(context.Context) (*docker.Client, error)
	Config   func() *config.Config
}

// NewCmdTasks creates the `clawker loop tasks` command.
func NewCmdTasks(f *cmdutil.Factory, runF func(context.Context, *TasksOptions) error) *cobra.Command {
	opts := &TasksOptions{
		IOStreams: f.IOStreams,
		Client:   f.Client,
		Config:   f.Config,
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
  clawker loop tasks --tasks todo.md --task-prompt-file instructions.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return tasksRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func tasksRun(_ context.Context, opts *TasksOptions) error {
	cs := opts.IOStreams.ColorScheme()
	fmt.Fprintf(opts.IOStreams.ErrOut, "%s loop tasks is not yet implemented\n", cs.WarningIcon())
	return nil
}
