package start

import (
	"context"
	"fmt"
	"os"

	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StartOptions holds options for the start command.
type StartOptions struct {
	Agent       string // Agent name to resolve container
	Attach      bool
	Interactive bool
}

// NewCmdStart creates the container start command.
func NewCmdStart(f *cmdutil.Factory) *cobra.Command {
	opts := &StartOptions{}

	cmd := &cobra.Command{
		Use:   "start [CONTAINER...]",
		Short: "Start one or more stopped containers",
		Long: `Starts one or more stopped clawker containers.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Start a stopped container by full name
  clawker container start clawker.myapp.ralph

  # Start a container using agent name (resolves via project config)
  clawker container start --agent ralph

  # Start multiple containers
  clawker container start clawker.myapp.ralph clawker.myapp.writer

  # Start and attach to container output
  clawker container start --attach clawker.myapp.ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Attach, "attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Attach container's STDIN")

	return cmd
}

func runStart(f *cmdutil.Factory, opts *StartOptions, containers []string) error {
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

	// Resolve container names
	containerNames, err := cmdutil.ResolveContainerNames(f, opts.Agent, containers)
	if err != nil {
		return err
	}

	var errs []error
	for _, name := range containerNames {
		if err := startContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			cmdutil.HandleError(err)
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
		return err
	}

	// Start the container
	// Note: --attach and --interactive would require additional implementation
	// to properly attach to container streams
	startOpts := dockerclient.ContainerStartOptions{}
	_, err = client.ContainerStart(ctx, c.ID, startOpts)
	return err
}
