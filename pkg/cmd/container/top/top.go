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

// NewCmd creates a new top command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "top CONTAINER [ps OPTIONS]",
		Short: "Display the running processes of a container",
		Long: `Display the running processes of a clawker container.

Additional arguments are passed directly to ps as options.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Show processes in a container
  clawker container top clawker.myapp.ralph

  # Show processes with custom ps options
  clawker container top clawker.myapp.ralph aux

  # Show all processes with extended info
  clawker container top clawker.myapp.ralph -ef`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, args[0], args[1:])
		},
	}

	return cmd
}

func run(_ *cmdutil.Factory, containerName string, psArgs []string) error {
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
