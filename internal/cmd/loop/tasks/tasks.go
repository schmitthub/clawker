// Package tasks provides the `clawker loop tasks` command.
package tasks

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// TasksOptions holds options for the loop tasks command.
type TasksOptions struct {
	*shared.LoopOptions

	// Factory DI
	IOStreams    *iostreams.IOStreams
	TUI          *tui.TUI
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	GitManager   func() (*git.GitManager, error)
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager
	Prompter     func() *prompter.Prompter
	Version      string

	// Task file (required)
	TasksFile string

	// Task prompt (mutually exclusive, optional — defaults to built-in template)
	TaskPrompt     string
	TaskPromptFile string

	// Output
	Format *cmdutil.FormatFlags

	// flags captures the command's FlagSet for Changed() detection
	flags *pflag.FlagSet
}

// NewCmdTasks creates the `clawker loop tasks` command.
func NewCmdTasks(f *cmdutil.Factory, runF func(context.Context, *TasksOptions) error) *cobra.Command {
	loopOpts := shared.NewLoopOptions()
	opts := &TasksOptions{
		LoopOptions:  loopOpts,
		IOStreams:    f.IOStreams,
		TUI:          f.TUI,
		Client:       f.Client,
		Config:       f.Config,
		GitManager:   f.GitManager,
		HostProxy:    f.HostProxy,
		SocketBridge: f.SocketBridge,
		Prompter:     f.Prompter,
		Version:      f.Version,
	}

	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Run an agent loop driven by a task file",
		Long: `Run Claude Code in an autonomous loop driven by a task file.

Each loop session gets an auto-generated agent name (e.g., loop-brave-turing).
A new container is created, hooks are injected, and the container is automatically
cleaned up when the loop exits. Each iteration, the agent reads the task file,
picks an open task, completes it, and marks it done. Clawker manages the loop —
the agent LLM handles task selection and completion.

The loop exits when:
  - All tasks are completed (agent signals via LOOP_STATUS)
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit`,
		Example: `  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Run with a custom task prompt template
  clawker loop tasks --tasks todo.md --task-prompt-file instructions.md

  # Run with a custom inline task prompt
  clawker loop tasks --tasks backlog.md --task-prompt "Pick the highest priority task"

  # Use a specific image
  clawker loop tasks --tasks todo.md --image node:20-slim

  # Stream all agent output in real time
  clawker loop tasks --tasks todo.md --verbose

  # Output final result as JSON
  clawker loop tasks --tasks todo.md --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.flags = cmd.Flags()
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

func tasksRun(ctx context.Context, opts *TasksOptions) error {
	ios := opts.IOStreams

	// 1. Resolve task prompt
	prompt, err := shared.ResolveTasksPrompt(opts.TasksFile, opts.TaskPrompt, opts.TaskPromptFile)
	if err != nil {
		return err
	}

	// 2. Auto-generate agent name for this loop session
	opts.Agent = loop.GenerateAgentName()

	// 3. Get config and Docker client
	cfgGateway := opts.Config()

	// 3a. Apply config file defaults for pre-runner fields (hooks_file, append_system_prompt)
	shared.ApplyLoopConfigDefaults(opts.LoopOptions, opts.flags, cfgGateway.Project.Loop)

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// 3.5. Check for concurrent sessions in the same directory
	// Use the project root (same as resolveWorkDir in CreateContainer) so that
	// the concurrency check matches the LabelWorkdir stored on containers.
	workDir := cfgGateway.Project.RootDir()
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
	}

	action, err := shared.CheckConcurrency(ctx, &shared.ConcurrencyCheckConfig{
		Client:    client,
		Project:   cfgGateway.Project.Project,
		WorkDir:   workDir,
		IOStreams: ios,
		Prompter:  opts.Prompter,
	})
	if err != nil {
		return err
	}
	switch action {
	case shared.ActionWorktree:
		if opts.Worktree == "" {
			spec, specErr := cmdutil.ParseWorktreeFlag("", opts.Agent)
			if specErr != nil {
				return fmt.Errorf("generating worktree name: %w", specErr)
			}
			opts.Worktree = spec.Branch
		}
	case shared.ActionAbort:
		return cmdutil.SilentError
	}

	// 4. Create and start container with hooks
	setup, cleanup, err := shared.SetupLoopContainer(ctx, &shared.LoopContainerConfig{
		Client:       client,
		Config:       cfgGateway,
		LoopOpts:     opts.LoopOptions,
		Flags:        opts.flags,
		Version:      opts.Version,
		GitManager:   opts.GitManager,
		HostProxy:    opts.HostProxy,
		SocketBridge: opts.SocketBridge,
		IOStreams:    ios,
	})
	if err != nil {
		return err
	}
	defer cleanup()

	// 5. Create runner
	runner, err := loop.NewRunner(client)
	if err != nil {
		return fmt.Errorf("creating loop runner: %w", err)
	}

	// 6. Build runner options
	runnerOpts := shared.BuildRunnerOptions(
		opts.LoopOptions, setup.ProjectCfg, setup.AgentName, setup.ContainerName, prompt, setup.WorkDir,
		opts.flags, cfgGateway.Project.Loop,
	)

	// 7. Run loop with appropriate output mode (TUI dashboard or text monitor)
	result, err := shared.RunLoop(ctx, shared.RunLoopConfig{
		Runner:      runner,
		RunnerOpts:  runnerOpts,
		TUI:         opts.TUI,
		IOStreams:   ios,
		Setup:       setup,
		Format:      opts.Format,
		Verbose:     opts.Verbose,
		CommandName: "tasks",
	})
	if err != nil {
		return err
	}

	// 8. Write result
	if writeErr := shared.WriteResult(ios.Out, ios.ErrOut, result, opts.Format); writeErr != nil {
		return writeErr
	}

	// 9. If loop ended with error, return SilentError (monitor/dashboard already displayed it)
	if result != nil && result.Error != nil {
		return cmdutil.SilentError
	}

	return nil
}
