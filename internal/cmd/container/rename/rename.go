// Package rename provides the container rename command.
package rename

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RenameOptions defines the options for the rename command.
type RenameOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent     bool // treat first argument as agent name(resolves to clawker.<project>.<agent>)
	container string
	newName   string
}

// NewCmdRename creates a new rename command.
func NewCmdRename(f *cmdutil.Factory, runF func(context.Context, *RenameOptions) error) *cobra.Command {
	opts := &RenameOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "rename CONTAINER NEW_NAME",
		Short: "Rename a container",
		Long: `Renames a clawker container.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration, and only NEW_NAME is required.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Rename a container using agent name
  clawker container rename --agent dev clawker.myapp.newname

  # Rename a container by full name
  clawker container rename clawker.myapp.dev clawker.myapp.newname`,
		Args: cmdutil.RequiresMinArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.container = args[0]
			opts.newName = args[1]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return renameRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat first argument as agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func renameRun(ctx context.Context, opts *RenameOptions) error {
	ios := opts.IOStreams
	oldName := opts.container
	newName := opts.newName

	if opts.Agent {
		var err error
		oldName, err = docker.ContainerName(opts.Config().Resolution.ProjectKey, oldName)
		if err != nil {
			return err
		}
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, oldName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", oldName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", oldName)
	}

	// Rename the container
	if _, err := client.ContainerRename(ctx, c.ID, newName); err != nil {
		return fmt.Errorf("renaming container %q to %q: %w", oldName, newName, err)
	}

	fmt.Fprintln(ios.Out, newName)
	return nil
}
