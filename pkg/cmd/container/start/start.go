package start

import (
	"context"
	"fmt"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StartOptions holds options for the start command.
type StartOptions struct {
	Attach      bool
	Interactive bool
}

// NewCmdStart creates the container start command.
func NewCmdStart(f *cmdutil.Factory) *cobra.Command {
	opts := &StartOptions{}

	cmd := &cobra.Command{
		Use:   "start CONTAINER [CONTAINER...]",
		Short: "Start one or more stopped containers",
		Long: `Starts one or more stopped clawker containers.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Start a stopped container
  clawker container start clawker.myapp.ralph

  # Start multiple containers
  clawker container start clawker.myapp.ralph clawker.myapp.writer

  # Start and attach to container output
  clawker container start --attach clawker.myapp.ralph`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Attach, "attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Attach container's STDIN")

	return cmd
}

func runStart(_ *cmdutil.Factory, opts *StartOptions, containers []string) error {
	ctx := context.Background()

	// Warn about unimplemented flags
	if opts.Attach {
		fmt.Fprintln(os.Stderr, "Warning: --attach flag is not yet implemented")
	}
	if opts.Interactive {
		fmt.Fprintln(os.Stderr, "Warning: --interactive flag is not yet implemented")
	}

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	var errs []error
	for _, name := range containers {
		if err := startContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d container(s)", len(errs))
	}
	return nil
}

func startContainer(ctx context.Context, client *docker.Client, name string, _ *StartOptions) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Start the container
	// Note: --attach and --interactive would require additional implementation
	// to properly attach to container streams
	startOpts := container.StartOptions{}
	return client.ContainerStart(ctx, c.ID, startOpts)
}
