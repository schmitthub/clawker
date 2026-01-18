// Package top provides the container top command.
package top

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the top command.
type Options struct {
	Agent string
}

// NewCmd creates a new top command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "top [CONTAINER] [ps OPTIONS]",
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
		Args: func(cmd *cobra.Command, args []string) error {
			agentFlag, _ := cmd.Flags().GetString("agent")
			if agentFlag != "" {
				// With --agent, all args are ps options
				return nil
			}
			if len(args) < 1 {
				return fmt.Errorf("requires at least 1 container argument or --agent flag")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, args []string) error {
	var containerName string
	var psArgs []string

	if opts.Agent != "" {
		// Resolve agent name
		containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, nil)
		if err != nil {
			return err
		}
		containerName = containers[0]
		psArgs = args // All args are ps options
	} else {
		containerName = args[0]
		psArgs = args[1:]
	}
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

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
		cmdutil.HandleError(err)
		return err
	}

	// Print output in table format
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Print header
	fmt.Fprintln(w, strings.Join(top.Titles, "\t"))

	// Print processes
	for _, proc := range top.Processes {
		fmt.Fprintln(w, strings.Join(proc, "\t"))
	}

	return w.Flush()
}
