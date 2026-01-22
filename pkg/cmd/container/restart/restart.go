// Package restart provides the container restart command.
package restart

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the restart command.
type Options struct {
	Agent   bool // treat arguments as agents names
	Timeout int
	Signal  string

	containers []string
}

// NewCmd creates a new restart command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

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
  clawker container restart --agent ralph

  # Restart a container by full name (10s timeout)
  clawker container restart clawker.myapp.ralph

  # Restart multiple containers
  clawker container restart clawker.myapp.ralph clawker.myapp.writer

  # Restart with a custom timeout (20 seconds)
  clawker container restart --time 20 --agent ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return run(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().IntVarP(&opts.Timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "", "Signal to send (default: SIGTERM)")

	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, opts *Options) error {
	// Resolve container names
	containers := opts.containers
	if opts.Agent {
		var err error
		containers, err = cmdutil.ResolveContainerNamesFromAgents(f, containers)
		if err != nil {
			return err
		}
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := restartContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to restart %d container(s)", len(errs))
	}
	return nil
}

func restartContainer(ctx context.Context, client *docker.Client, name string, opts *Options) error {
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
