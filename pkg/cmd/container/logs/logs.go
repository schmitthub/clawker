package logs

import (
	"context"
	"fmt"
	"io"
	"os"

	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// LogsOptions holds options for the logs command.
type LogsOptions struct {
	Agent      string
	Follow     bool
	Timestamps bool
	Details    bool
	Since      string
	Until      string
	Tail       string
}

// NewCmdLogs creates the container logs command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	opts := &LogsOptions{}

	cmd := &cobra.Command{
		Use:   "logs [CONTAINER]",
		Short: "Fetch the logs of a container",
		Long: `Fetches the logs of a clawker container.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Show logs using agent name
  clawker container logs --agent ralph

  # Show logs by full container name
  clawker container logs clawker.myapp.ralph

  # Follow log output (like tail -f)
  clawker container logs --follow --agent ralph

  # Show last 50 lines
  clawker container logs --tail 50 --agent ralph

  # Show logs since a timestamp
  clawker container logs --since 2024-01-01T00:00:00Z --agent ralph

  # Show logs with timestamps
  clawker container logs --timestamps --agent ralph`,
		Args: cmdutil.AgentArgsValidatorExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Follow, "follow", "f", false, "Follow log output")
	cmd.Flags().BoolVarP(&opts.Timestamps, "timestamps", "t", false, "Show timestamps")
	cmd.Flags().BoolVar(&opts.Details, "details", false, "Show extra details provided to logs")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Show logs since timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)")
	cmd.Flags().StringVar(&opts.Until, "until", "", "Show logs before timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)")
	cmd.Flags().StringVar(&opts.Tail, "tail", "all", "Number of lines to show from the end (default: all)")

	return cmd
}

func runLogs(f *cmdutil.Factory, opts *LogsOptions, args []string) error {
	ctx := context.Background()

	// Resolve container name
	containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, args)
	if err != nil {
		return err
	}
	containerName := containers[0]

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
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

	// Build log options
	logOpts := dockerclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Details:    opts.Details,
		Since:      opts.Since,
		Until:      opts.Until,
		Tail:       opts.Tail,
	}

	// Get logs
	reader, err := client.ContainerLogs(ctx, c.ID, logOpts)
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}
	defer reader.Close()

	// Stream logs to stdout
	if _, err = io.Copy(os.Stdout, reader); err != nil {
		return fmt.Errorf("error streaming logs: %w", err)
	}
	return nil
}
