// Package top provides the container top command.
package top

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// TopOptions holds options for the top command.
type TopOptions struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Config func() *config.Config

	Agent bool

	Args []string
}

// NewCmdTop creates a new top command.
func NewCmdTop(f *cmdutil.Factory, runF func(context.Context, *TopOptions) error) *cobra.Command {
	opts := &TopOptions{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Config: f.Config,
	}

	cmd := &cobra.Command{
		Use:   "top CONTAINER [ps OPTIONS]",
		Short: "Display the running processes of a container",
		Long: `Display the running processes of a clawker container.

Additional arguments are passed directly to ps as options.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Show processes using agent name
  clawker container top --agent ralph

  # Show processes by full container name
  clawker container top clawker.myapp.ralph

  # Show processes with custom ps options
  clawker container top --agent ralph aux

  # Show all processes with extended info
  clawker container top --agent ralph -ef`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return topRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat first argument as agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func topRun(ctx context.Context, opts *TopOptions) error {
	ios := opts.IOStreams

	// First arg is container/agent name, rest are ps options
	containerName := opts.Args[0]
	psArgs := opts.Args[1:]

	if opts.Agent {
		// Resolve agent name to full container name
		containers := docker.ContainerNamesFromAgents(opts.Config().Resolution().ProjectKey, []string{containerName})
		containerName = containers[0]
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// Get top output
	top, err := client.ContainerTop(ctx, c.ID, psArgs)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Print output in table format
	w := tabwriter.NewWriter(ios.Out, 0, 0, 3, ' ', 0)

	// Print header
	fmt.Fprintln(w, strings.Join(top.Titles, "\t"))

	// Print processes
	for _, proc := range top.Processes {
		fmt.Fprintln(w, strings.Join(proc, "\t"))
	}

	return w.Flush()
}
