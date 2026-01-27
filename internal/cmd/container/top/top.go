// Package top provides the container top command.
package top

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the top command.
type Options struct {
	Agent bool

	args []string
}

// NewCmd creates a new top command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

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
		Args: cmdutil2.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.args = args
			return run(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat first argument as agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func run(ctx context.Context, f *cmdutil2.Factory, opts *Options) error {
	ios := f.IOStreams

	// First arg is container/agent name, rest are ps options
	containerName := opts.args[0]
	psArgs := opts.args[1:]

	if opts.Agent {
		// Resolve agent name to full container name
		containers, err := cmdutil2.ResolveContainerNamesFromAgents(f, []string{containerName})
		if err != nil {
			return err
		}
		containerName = containers[0]
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(ios, err)
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
		cmdutil2.HandleError(ios, err)
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
