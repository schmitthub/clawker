package kill

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// KillOptions holds options for the kill command.
type KillOptions struct {
	Signal string
}

// NewCmdKill creates the container kill command.
func NewCmdKill(f *cmdutil.Factory) *cobra.Command {
	opts := &KillOptions{}

	cmd := &cobra.Command{
		Use:   "kill CONTAINER [CONTAINER...]",
		Short: "Kill one or more running containers",
		Long: `Kills one or more running clawker containers.

The main process inside the container is sent SIGKILL signal (default),
or the signal specified with the --signal option.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Kill a container (SIGKILL)
  clawker container kill clawker.myapp.ralph

  # Kill multiple containers
  clawker container kill clawker.myapp.ralph clawker.myapp.writer

  # Send specific signal
  clawker container kill --signal SIGTERM clawker.myapp.ralph
  clawker container kill -s SIGINT clawker.myapp.ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKill(f, opts, args)
		},
	}

	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "SIGKILL", "Signal to send to the container")

	return cmd
}

func runKill(_ *cmdutil.Factory, opts *KillOptions, containers []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	var errs []error
	for _, name := range containers {
		if err := killContainer(ctx, client, name, opts.Signal); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to kill %d container(s)", len(errs))
	}
	return nil
}

func killContainer(ctx context.Context, client *docker.Client, name, signal string) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Kill the container with signal
	_, err = client.ContainerKill(ctx, c.ID, signal)
	return err
}
