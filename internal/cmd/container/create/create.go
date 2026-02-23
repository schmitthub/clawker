// Package create provides the container create command.
package create

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CreateOptions holds options for the create command.
// It embeds ContainerOptions for shared container configuration.
type CreateOptions struct {
	*shared.ContainerOptions

	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	Client         func(context.Context) (*docker.Client, error)
	Config         func() (config.Config, error)
	ProjectManager func() (project.ProjectManager, error)
	HostProxy      func() hostproxy.HostProxyService
	Prompter       func() *prompter.Prompter
	Version        string

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmdCreate creates a new container create command.
func NewCmdCreate(f *cmdutil.Factory, runF func(context.Context, *CreateOptions) error) *cobra.Command {
	containerOpts := shared.NewContainerOptions()
	opts := &CreateOptions{
		ContainerOptions: containerOpts,
		IOStreams:        f.IOStreams,
		TUI:              f.TUI,
		Client:           f.Client,
		Config:           f.Config,
		ProjectManager:   f.ProjectManager,
		HostProxy:        f.HostProxy,
		Prompter:         f.Prompter,
		Version:          f.Version,
	}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create a new container",
		Long: `Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from .clawker.yaml. When --name is provided, it overrides this.

If IMAGE is "@", clawker will resolve the project's built image with :latest tag.`,
		Example: `  # Create a container with a specific agent name and interactive TTY
  clawker container create -it --agent ralph @ 

  # Create a container with a specific claude code entry flag and interactive TTY
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
	containerOpts := opts.ContainerOptions
	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg := cfgGateway.Project()

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

	if containerOpts.Image == "@" {
		resolvedImage, err := client.ResolveImageWithSource(ctx, projectName)
		if err != nil {
			return fmt.Errorf("resolving image: %w", err)
		}
		if resolvedImage == nil {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s No image specified and no project image found\n", cs.FailureIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. Specify an image: clawker container create IMAGE")
			fmt.Fprintln(ios.ErrOut, "  2. Build a project image: clawker build")
			return cmdutil.SilentError
		}

		containerOpts.Image = resolvedImage.Reference
	}

	// Defensive check: --name and --agent should not both be set
	if containerOpts.Name != "" && containerOpts.Agent != "" && containerOpts.Name != containerOpts.Agent {
		return fmt.Errorf("cannot use both --name and --agent")
	}

	// --- Phase B: Create container with spinner ---

	events := make(chan shared.CreateContainerEvent, 16)
	type outcome struct {
		result *shared.CreateContainerResult
		err    error
	}
	done := make(chan outcome, 1)

	go func() {
		defer close(events)
		r, err := shared.CreateContainer(ctx, &shared.CreateContainerConfig{
			Client:         client,
			Cfg:            cfgGateway,
			Config:         cfg,
			ProjectName:    projectName,
			Options:        containerOpts,
			Flags:          opts.flags,
			Version:        opts.Version,
			ProjectManager: opts.ProjectManager,
			HostProxy:      opts.HostProxy,
			Logger:         ios.Logger,
			Is256Color:     ios.Is256ColorSupported(),
			IsTrueColor:    ios.IsTrueColorSupported(),
		}, events)
		done <- outcome{r, err}
	}()

	var warnings []string
	for ev := range events {
		switch {
		case ev.Type == shared.MessageWarning:
			warnings = append(warnings, ev.Message)
		case ev.Status == shared.StepRunning:
			ios.StartSpinner(ev.Message)
		}
	}
	ios.StopSpinner()

	o := <-done
	if o.err != nil {
		return o.err
	}

	// --- Phase C: Post-progress ---

	cs := ios.ColorScheme()
	for _, w := range warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w)
	}

	fmt.Fprintln(ios.Out, o.result.ContainerID[:12])
	return nil
}
