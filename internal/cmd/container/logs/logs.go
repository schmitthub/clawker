package logs

import (
	"context"
	"fmt"
	"io"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// LogsOptions holds options for the logs command.
type LogsOptions struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Resolution func() *config.Resolution

	Agent      bool
	Follow     bool
	Timestamps bool
	Details    bool
	Since      string
	Until      string
	Tail       string

	containers []string
}

// NewCmdLogs creates the container logs command.
func NewCmdLogs(f *cmdutil.Factory, runF func(context.Context, *LogsOptions) error) *cobra.Command {
	opts := &LogsOptions{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Resolution: f.Resolution,
	}

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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return runLogs(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat argument as agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Follow, "follow", "f", false, "Follow log output")
	cmd.Flags().BoolVarP(&opts.Timestamps, "timestamps", "t", false, "Show timestamps")
	cmd.Flags().BoolVar(&opts.Details, "details", false, "Show extra details provided to logs")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Show logs since timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)")
	cmd.Flags().StringVar(&opts.Until, "until", "", "Show logs before timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)")
	cmd.Flags().StringVar(&opts.Tail, "tail", "all", "Number of lines to show from the end (default: all)")

	return cmd
}

func runLogs(ctx context.Context, opts *LogsOptions) error {
	ios := opts.IOStreams

	// Resolve container name
	containerName := opts.containers[0]
	if opts.Agent {
		containers := docker.ContainerNamesFromAgents(opts.Resolution().ProjectKey, opts.containers)
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

	// Build log options
	logOpts := docker.ContainerLogsOptions{
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
	if _, err = io.Copy(ios.Out, reader); err != nil {
		return fmt.Errorf("error streaming logs: %w", err)
	}
	return nil
}
