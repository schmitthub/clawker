package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/cobra"
)

// RemoveOptions holds options for the remove command.
type RemoveOptions struct {
	IOStreams    *iostreams.IOStreams
	Client       func(context.Context) (*docker.Client, error)
	Config       func() (config.Config, error)
	SocketBridge func() socketbridge.SocketBridgeManager

	Agent   bool
	Force   bool
	Volumes bool

	Containers []string
}

// NewCmdRemove creates the container remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:    f.IOStreams,
		Client:       f.Client,
		Config:       f.Config,
		SocketBridge: f.SocketBridge,
	}

	cmd := &cobra.Command{
		Use:     "remove [OPTIONS] CONTAINER [CONTAINER...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more containers",
		Long: `Removes one or more clawker containers.

By default, only stopped containers can be removed. Use --force to remove
running containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Remove a container using agent name
  clawker container remove --agent dev

  # Remove a stopped container by full name
  clawker container remove clawker.myapp.dev

  # Remove multiple containers
  clawker container rm clawker.myapp.dev clawker.myapp.writer

  # Force remove a running container
  clawker container remove --force --agent dev

  # Remove container and its volumes
  clawker container remove --volumes --agent dev`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers")
	cmd.Flags().BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove associated volumes")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		cfg, err := opts.Config()
		if err != nil {
			return err
		}
		var project string
		if p := cfg.Project(); p != nil {
			project = p.Name
		}
		resolved, err := docker.ContainerNamesFromAgents(project, containers)
		if err != nil {
			return err
		}
		containers = resolved
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	var errs []error
	for _, name := range containers {
		if err := removeContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
	}
	return nil
}

func removeContainer(ctx context.Context, client *docker.Client, name string, opts *RemoveOptions) error {
	// Find container by name
	container, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Stop socket bridge before removing the container (best-effort)
	if opts.SocketBridge != nil {
		if mgr := opts.SocketBridge(); mgr != nil {
			if err := mgr.StopBridge(container.ID); err != nil {
				opts.IOStreams.Logger.Warn().Err(err).Str("container", container.ID).Msg("failed to stop socket bridge")
			}
		}
	}

	// Use RemoveContainerWithVolumes if volumes flag is set
	if opts.Volumes {
		return client.RemoveContainerWithVolumes(ctx, container.ID, opts.Force)
	}

	// Otherwise just remove the container
	_, err = client.ContainerRemove(ctx, container.ID, opts.Force)
	return err
}
