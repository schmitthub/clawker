// Package wait provides the container wait command.
package wait

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// Options defines the options for the wait command.
type Options struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Resolution func() *config.Resolution

	Agent      bool
	Containers []string
}

// NewCmd creates a new wait command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Resolution: f.Resolution,
	}

	cmd := &cobra.Command{
		Use:   "wait [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Block until one or more containers stop, then print their exit codes",
		Long: `Blocks until one or more clawker containers stop, then prints their exit codes.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Wait for a container using agent name
  clawker container wait --agent ralph

  # Wait for a container by full name
  clawker container wait clawker.myapp.ralph

  # Wait for multiple containers
  clawker container wait clawker.myapp.ralph clawker.myapp.writer`,
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			return run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Use agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func run(ctx context.Context, opts *Options) error {
	ios := opts.IOStreams

	// Resolve container names
	// When opts.Agent is true, all items in opts.Containers are agent names
	containers := opts.Containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Resolution().ProjectKey, containers)
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	var errs []error
	for _, name := range containers {
		exitCode, err := waitContainer(ctx, client, name)
		if err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)
		} else {
			// Print exit code to stdout (for scripting)
			fmt.Fprintln(ios.Out, exitCode)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to wait for %d container(s)", len(errs))
	}
	return nil
}

func waitContainer(ctx context.Context, client *docker.Client, name string) (int64, error) {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return 0, fmt.Errorf("container %q not found", name)
	}

	// Wait for the container to stop
	waitResult := client.ContainerWait(ctx, c.ID, container.WaitConditionNotRunning)

	select {
	case err := <-waitResult.Error:
		if err != nil {
			return 0, err
		}
	case status := <-waitResult.Result:
		return status.StatusCode, nil
	}

	return 0, nil
}
