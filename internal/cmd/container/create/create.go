// Package create provides the container create command.
package create

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
)

// CreateOptions holds options for the create command.
// It embeds ContainerCreateOptions for shared container configuration.
type CreateOptions struct {
	*shared.ContainerCreateOptions

	IOStreams       *iostreams.IOStreams
	TUI             *tui.TUI
	Client          func(context.Context) (*docker.Client, error)
	Config          func() (config.Config, error)
	ProjectManager  func() (project.ProjectManager, error)
	ProjectRegistry func() (*project.Registry, error)
	HostProxy       func() hostproxy.Service
	Prompter        func() *prompter.Prompter
	Logger          func() (*logger.Logger, error)
	BundleManager   func() (*bundle.Manager, error)
	Version         string

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmdCreate creates a new container create command.
func NewCmdCreate(f *cmdutil.Factory, runF func(context.Context, *CreateOptions) error) *cobra.Command {
	containerOpts := shared.NewContainerOptions()
	opts := &CreateOptions{
		ContainerCreateOptions: containerOpts,
		IOStreams:              f.IOStreams,
		TUI:                    f.TUI,
		Client:                 f.Client,
		Config:                 f.Config,
		ProjectManager:         f.ProjectManager,
		ProjectRegistry:        f.ProjectRegistry,
		HostProxy:              f.HostProxy,
		Prompter:               f.Prompter,
		Logger:                 f.Logger,
		BundleManager:          f.BundleManager,
		Version:                f.Version,
	}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create a new container",
		Long: `Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project is resolved from the current directory. When --name is provided, it overrides this.

If IMAGE is "@", clawker resolves the built image for the current scope: the
project image inside a registered project, or the global image (built with
"clawker build" outside any project) elsewhere. "@" selects the default
harness image; "@:<harness>" (e.g. "@:codex") selects a specific harness
image built with "clawker build -t <harness>".`,
		Example: `  # Create a container with a specific agent name and interactive TTY
  clawker container create -it --agent ralph @ 

  # Create a container passing a harness entry flag with an interactive TTY
  clawker container create -it --agent myagent @ --dangerously-skip-permissions
  
  # Create a container with environment variables and ports
  clawker container create -it --agent web -e PORT=8080 -p 8080:8080 @
`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerOpts.Image = args[0]
			if len(args) > 1 {
				containerOpts.Command = args[1:]
			}
			opts.flags = cmd.Flags()
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return createRun(cmd.Context(), opts)
		},
	}

	// Add shared container flags
	shared.AddFlags(cmd.Flags(), containerOpts)
	shared.MarkMutuallyExclusive(cmd)

	// Stop parsing flags after the first positional argument (IMAGE).
	// This allows flags after IMAGE to be passed to the container command.
	// Example: clawker create alpine --version
	//   - "alpine" is IMAGE
	//   - "--version" is passed to the container (not parsed as clawker flag)
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func createRun(ctx context.Context, opts *CreateOptions) error {
	ios := opts.IOStreams
	containerOpts := opts.ContainerCreateOptions

	// Opt-in bundle auto-update before the container resolves its harness/egress
	// floor against the cached bundle set. Warn and proceed.
	cmdutil.RunBundleAutoUpdate(ctx, opts.BundleManager, ios)

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// --- Phase A: Pre-progress (synchronous) ---

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Resolve project name from ProjectManager (empty if no project registered)
	var projectName string
	if opts.ProjectManager != nil {
		if pm, pmErr := opts.ProjectManager(); pmErr == nil {
			if p, pErr := pm.CurrentProject(ctx); pErr == nil {
				projectName = p.Name()
			}
		}
	}

	if harnessTag, isPlaceholder := shared.ParseImagePlaceholder(containerOpts.Image); isPlaceholder {
		ref, resolveErr := shared.ResolvePlaceholderImage(
			ctx, client, cfg, ios, projectName, harnessTag, "create")
		if resolveErr != nil {
			return fmt.Errorf("resolving image: %w", resolveErr)
		}
		containerOpts.Image = ref
	}

	// Defensive check: --name and --agent should not both be set
	if containerOpts.Name != "" && containerOpts.Agent != "" && containerOpts.Name != containerOpts.Agent {
		return fmt.Errorf("cannot use both --name and --agent")
	}

	// Warn if workspace mount would include the home directory or higher
	if shared.IsOutsideHome(".") {
		confirmed, promptErr := opts.Prompter().Confirm(
			"WARNING: This will mount your entire home directory (or higher) into a container. Continue?",
			false,
		)
		if promptErr != nil {
			return promptErr
		}
		if !confirmed {
			return cmdutil.SilentError
		}
	}

	type outcome struct {
		result *shared.CreateContainerResult
		err    error
	}
	done := make(chan outcome, 1)

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	go func() {
		r, err := shared.CreateContainer(ctx, &shared.CreateContainerOptions{
			Client:          client,
			Config:          cfg,
			ProjectName:     projectName,
			Options:         containerOpts,
			Flags:           opts.flags,
			Version:         opts.Version,
			ProjectManager:  opts.ProjectManager,
			ProjectRegistry: opts.ProjectRegistry,
			HostProxy:       opts.HostProxy,
			Log:             log,
			Is256Color:      ios.Is256ColorSupported(),
			IsTrueColor:     ios.IsTrueColorSupported(),
		})
		done <- outcome{r, err}
	}()

	ios.StopSpinner()

	o := <-done
	if o.err != nil {
		return o.err
	}

	fmt.Fprintln(ios.Out, o.result.ContainerID[:12])
	return nil
}
