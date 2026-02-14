// Package iterate provides the `clawker loop iterate` command.
package iterate

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
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// IterateOptions holds options for the loop iterate command.
type IterateOptions struct {
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

	// AppendSystemPrompt allows users to override clawkers loop system prompt to be appended to the default system prompt for each iteration.
	AppendSystemPrompt string

	// Prompt source (mutually exclusive, one required)
	Prompt     string
	PromptFile string

	// Output
	Format *cmdutil.FormatFlags

	// flags captures the command's FlagSet for Changed() detection
	flags *pflag.FlagSet
}

// NewCmdIterate creates the `clawker loop iterate` command.
func NewCmdIterate(f *cmdutil.Factory, runF func(context.Context, *IterateOptions) error) *cobra.Command {
	loopOpts := shared.NewLoopOptions()
	opts := &IterateOptions{
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
		Use:   "iterate",
		Short: "Run an agent loop with a repeated prompt",
		Long: `Run Claude Code in an autonomous loop, repeating the same prompt each iteration.

Each loop session gets an auto-generated agent name (e.g., loop-brave-turing).
A new container is created, hooks are injected, and the container is automatically
cleaned up when the loop exits. Each iteration starts a fresh Claude session
(no conversation context carried forward). The agent only sees the current
codebase state from previous runs.

The loop exits when:
  - Claude signals completion via a LOOP_STATUS block
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit`,
		Example: `  # Run a loop with a prompt
  clawker loop iterate --prompt "Fix all failing tests"

  # Run with a prompt from a file
  clawker loop iterate --prompt-file task.md

  # Run with custom loop limits
  clawker loop iterate --prompt "Refactor auth module" --max-loops 100

  # Stream all agent output in real time
  clawker loop iterate --prompt "Add tests" --verbose

  # Run in a git worktree for isolation
  clawker loop iterate --prompt "Refactor auth" --worktree feature/auth

  # Use a specific image
  clawker loop iterate --prompt "Fix tests" --image node:20-slim

  # Output final result as JSON
  clawker loop iterate --prompt "Fix tests" --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.flags = cmd.Flags()
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return iterateRun(cmd.Context(), opts)
		},
	}

	// Prompt source flags
	cmd.Flags().StringVarP(&opts.Prompt, "prompt", "p", "", "Prompt to repeat each iteration")
	cmd.Flags().StringVar(&opts.PromptFile, "prompt-file", "", "Path to file containing the prompt")
	cmd.Flags().StringVarP(&opts.AppendSystemPrompt, "append-system-prompt", "", "", "Additional system prompt to append to the default system prompt")

	// Shared loop flags
	shared.AddLoopFlags(cmd, loopOpts)

	// Output format flags (--json, --quiet, --format)
	opts.Format = cmdutil.AddFormatFlags(cmd)

	// Requirements and mutual exclusivity
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagsOneRequired("prompt", "prompt-file")
	shared.MarkVerboseExclusive(cmd)

	return cmd
}

func iterateRun(ctx context.Context, opts *IterateOptions) error {
	ios := opts.IOStreams

	// 1. Resolve prompt
	prompt, err := shared.ResolvePrompt(opts.Prompt, opts.PromptFile)
	if err != nil {
		return err
	}

	// 2. Auto-generate agent name for this loop session
	opts.Agent = shared.GenerateAgentName()

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
	// TODO: Does this rely on the assumption that the command is being ran from within a project dir? ProjectCfg worktrees are not in the original root dir so this is a poor assumption. What if a user runs a loop in the same worktree?
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

	// 4. Resolve image once
	image, err := shared.ResolveLoopImage(ctx, client, ios, opts.LoopOptions)
	if err != nil {
		return err
	}
	opts.Image = image

	// Container command
	cmd := []string{
		"-p", prompt,
		"--output-format=stream-json",
		"--verbose",
	}
	if opts.SkipPermissions {
		cmd = append(cmd, "--dangerously-skip-permissions")
	}
	if opts.AppendSystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", opts.AppendSystemPrompt)
	}

	// 5. Build per-iteration container factory
	createContainer := shared.MakeCreateContainerFunc(&shared.LoopContainerConfig{
		Client:       client,
		Command:      cmd,
		Config:       cfgGateway,
		LoopOpts:     opts.LoopOptions,
		Flags:        opts.flags,
		Version:      opts.Version,
		GitManager:   opts.GitManager,
		HostProxy:    opts.HostProxy,
		SocketBridge: opts.SocketBridge,
		IOStreams:    ios,
	})

	// 6. Create runner
	runner, err := shared.NewRunner(client)
	if err != nil {
		return fmt.Errorf("creating loop runner: %w", err)
	}

	// 7. Build runner options
	runnerOpts := shared.BuildRunnerOptions(
		opts.LoopOptions, cfgGateway.Project, opts.Agent, prompt, workDir,
		createContainer, opts.flags, cfgGateway.Project.Loop,
	)

	// Setup info for dashboard/monitor display (no container ID — per-iteration)
	setup := &shared.LoopContainerResult{
		AgentName:  opts.Agent,
		ProjectCfg: cfgGateway.Project,
		WorkDir:    workDir,
	}

	// 8. Run loop with appropriate output mode (TUI dashboard or text monitor)
	result, err := shared.RunLoop(ctx, shared.RunLoopConfig{
		Runner:      runner,
		RunnerOpts:  runnerOpts,
		TUI:         opts.TUI,
		IOStreams:   ios,
		Setup:       setup,
		Format:      opts.Format,
		Verbose:     opts.Verbose,
		CommandName: "iterate",
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
