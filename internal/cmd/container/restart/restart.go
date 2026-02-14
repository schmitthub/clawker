// Package restart provides the container restart command.
package restart

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RestartOptions defines the options for the restart command.
type RestartOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent      bool // treat arguments as agents names
	Timeout    int
	Signal     string
	Containers []string
}

// NewCmdRestart creates a new restart command.
func NewCmdRestart(f *cmdutil.Factory, runF func(context.Context, *RestartOptions) error) *cobra.Command {
	opts := &RestartOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "restart [CONTAINER...]",
		Short: "Restart one or more containers",
		Long: `Restarts one or more clawker containers.

The container is stopped with a timeout period (default 10s), then started again.
If --signal is specified, that signal is sent instead of SIGTERM.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Restart a container using agent name
  clawker container restart --agent dev

  # Restart a container by full name (10s timeout)
  clawker container restart clawker.myapp.dev

  # Restart multiple containers
  clawker container restart clawker.myapp.dev clawker.myapp.writer

  # Restart with a custom timeout (20 seconds)
  clawker container restart --time 20 --agent dev`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return restartRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().IntVarP(&opts.Timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "", "Signal to send (default: SIGTERM)")

	return cmd
}

func restartRun(ctx context.Context, opts *RestartOptions) error {
	ios := opts.IOStreams

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

	cs := ios.ColorScheme()
	var errs []error
	for _, name := range containers {
		if err := restartContainer(ctx, client, name, opts); err != nil {
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

func restartContainer(ctx context.Context, client *docker.Client, name string, opts *RestartOptions) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// If signal specified, kill with that signal first, then start
	if opts.Signal != "" {
		if _, err := client.ContainerKill(ctx, c.ID, opts.Signal); err != nil {
			return err
		}
		_, err = client.ContainerStart(ctx, docker.ContainerStartOptions{
			ContainerID: c.ID,
			EnsureNetwork: &docker.EnsureNetworkOptions{
				Name: docker.NetworkName,
			},
		})
		return err
	}

	// Restart the container with timeout
	timeout := opts.Timeout
	_, err = client.ContainerRestart(ctx, c.ID, &timeout)
	return err
}
