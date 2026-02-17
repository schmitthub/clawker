package stop

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

// StopOptions holds options for the stop command.
type StopOptions struct {
	IOStreams    *iostreams.IOStreams
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	SocketBridge func() socketbridge.SocketBridgeManager

	Agent   bool
	Timeout int
	Signal  string

	Containers []string
}

// NewCmdStop creates the container stop command.
func NewCmdStop(f *cmdutil.Factory, runF func(context.Context, *StopOptions) error) *cobra.Command {
	opts := &StopOptions{
		IOStreams:    f.IOStreams,
		Client:       f.Client,
		Config:       f.Config,
		SocketBridge: f.SocketBridge,
	}

	cmd := &cobra.Command{
		Use:   "stop [CONTAINER...]",
		Short: "Stop one or more running containers",
		Long: `Stops one or more running clawker containers.

The container is sent a SIGTERM signal, then after a timeout period (default 10s),
it is sent SIGKILL if still running.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Stop a container using agent name (resolves via project config)
  clawker container stop --agent dev

  # Stop a container by full name (10s timeout)
  clawker container stop clawker.myapp.dev

  # Stop multiple containers
  clawker container stop clawker.myapp.dev clawker.myapp.writer

  # Stop with a custom timeout (20 seconds)
  clawker container stop --time 20 --agent dev`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return stopRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().IntVarP(&opts.Timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "", "Signal to send (default: SIGTERM)")

	return cmd
}

func stopRun(ctx context.Context, opts *StopOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		resolved, err := docker.ContainerNamesFromAgents(opts.Config().Resolution.ProjectKey, containers)
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
		if err := stopContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
	}
	return nil
}

func stopContainer(ctx context.Context, client *docker.Client, name string, opts *StopOptions) error {
	// Find container by name
	container, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Stop socket bridge before stopping the container (best-effort)
	if opts.SocketBridge != nil {
		if mgr := opts.SocketBridge(); mgr != nil {
			if err := mgr.StopBridge(container.ID); err != nil {
				opts.IOStreams.Logger.Warn().Err(err).Str("container", container.ID).Msg("failed to stop socket bridge")
			}
		}
	}

	// If signal specified, send that signal instead of using stop
	if opts.Signal != "" {
		_, err = client.ContainerKill(ctx, container.ID, opts.Signal)
		return err
	}

	// Stop the container with timeout
	timeout := opts.Timeout
	_, err = client.ContainerStop(ctx, container.ID, &timeout)
	return err
}
