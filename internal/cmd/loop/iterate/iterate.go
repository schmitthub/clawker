// Package iterate provides the `clawker loop iterate` command.
package iterate

import (
	"context"
	"fmt"

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

// IterateOptions holds options for the loop iterate command.
type IterateOptions struct {
	*shared.LoopOptions

	// Factory DI
	IOStreams     *iostreams.IOStreams
	TUI          *tui.TUI
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	GitManager   func() (*git.GitManager, error)
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager
	Prompter     func() *prompter.Prompter
	Version      string

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
		TUI:         f.TUI,
		Client:      f.Client,
		Config:      f.Config,
		GitManager:  f.GitManager,
		HostProxy:   f.HostProxy,
		SocketBridge: f.SocketBridge,
		Prompter:    f.Prompter,
		Version:     f.Version,
	}

	cmd := &cobra.Command{
		Use:   "iterate",
		Short: "Run an agent loop with a repeated prompt",
		Long: `Run Claude Code in an autonomous loop, repeating the same prompt each iteration.

A new container is created for the loop session, hooks are injected, and the
container is automatically cleaned up when the loop exits. Each iteration starts
a fresh Claude session (no conversation context carried forward). The agent only
sees the current codebase state from previous runs.

The loop exits when:
  - Claude signals completion via a LOOP_STATUS block
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit`,
		Example: `  # Run a loop with a prompt
  clawker loop iterate --agent dev --prompt "Fix all failing tests"

  # Run with a prompt from a file
  clawker loop iterate --agent dev --prompt-file task.md

  # Run with custom loop limits
  clawker loop iterate --agent dev --prompt "Refactor auth module" --max-loops 100

  # Stream all agent output in real time
  clawker loop iterate --agent dev --prompt "Add tests" --verbose

  # Run in a git worktree for isolation
  clawker loop iterate --agent dev --prompt "Refactor auth" --worktree feature/auth

  # Use a specific image
  clawker loop iterate --agent dev --prompt "Fix tests" --image node:20-slim

  # Output final result as JSON
  clawker loop iterate --agent dev --prompt "Fix tests" --json`,
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

	// Shared loop flags
	shared.AddLoopFlags(cmd, loopOpts)

	// Output format flags (--json, --quiet, --format)
	opts.Format = cmdutil.AddFormatFlags(cmd)

	// Requirements and mutual exclusivity
	_ = cmd.MarkFlagRequired("agent")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagsOneRequired("prompt", "prompt-file")
	shared.MarkVerboseExclusive(cmd)

	return cmd
}

func iterateRun(ctx context.Context, opts *IterateOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// 1. Resolve prompt
	prompt, err := shared.ResolvePrompt(opts.Prompt, opts.PromptFile)
	if err != nil {
		return err
	}

	// 2. Get config and Docker client
	cfgGateway := opts.Config()

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// 3. Create and start container with hooks
	setup, cleanup, err := shared.SetupLoopContainer(ctx, &shared.LoopContainerConfig{
		Client:       client,
		Config:       cfgGateway,
		LoopOpts:     opts.LoopOptions,
		Flags:        opts.flags,
		Version:      opts.Version,
		GitManager:   opts.GitManager,
		HostProxy:    opts.HostProxy,
		SocketBridge: opts.SocketBridge,
		IOStreams:     ios,
	})
	if err != nil {
		return err
	}
	defer cleanup()

	// 4. Create runner
	runner, err := loop.NewRunner(client)
	if err != nil {
		return fmt.Errorf("creating loop runner: %w", err)
	}

	// 5. Build runner options
	runnerOpts := shared.BuildRunnerOptions(
		opts.LoopOptions, setup.Project, setup.AgentName, setup.ContainerName, prompt,
		opts.flags, cfgGateway.Project.Loop,
	)

	// 6. Set up monitor
	monitor := loop.NewMonitor(loop.MonitorOptions{
		Writer:   ios.ErrOut,
		MaxLoops: runnerOpts.MaxLoops,
		Verbose:  opts.Verbose,
	})
	runnerOpts.Monitor = monitor

	// 7. If verbose, stream output chunks to stderr
	if opts.Verbose {
		runnerOpts.OnOutput = func(chunk []byte) {
			_, _ = ios.ErrOut.Write(chunk)
		}
	}

	// 8. Print start message
	fmt.Fprintf(ios.ErrOut, "%s Starting loop iterate for %s.%s (%d max loops)\n",
		cs.InfoIcon(), setup.Project, setup.AgentName, runnerOpts.MaxLoops)

	// 9. Run the loop
	result, err := runner.Run(ctx, runnerOpts)
	if err != nil {
		return err
	}

	// 10. Write result
	if writeErr := shared.WriteResult(ios.Out, ios.ErrOut, result, opts.Format); writeErr != nil {
		return writeErr
	}

	// 11. If loop ended with error, return SilentError (monitor already displayed it)
	if result.Error != nil {
		return cmdutil.SilentError
	}

	return nil
}
