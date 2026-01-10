package logs

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// LogsOptions contains the options for the logs command.
type LogsOptions struct {
	Agent  string
	Follow bool
	Tail   string
}

// NewCmdLogs creates the logs command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	opts := &LogsOptions{}

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream container logs",
		Long: `Shows logs from a Claude container.

If multiple containers exist and --agent is not specified, you must specify which agent.

Examples:
  claucker logs                   # Show logs (if single container)
  claucker logs --agent ralph     # Show logs for specific agent
  claucker logs -f                # Follow log output (like tail -f)
  claucker logs --tail 50         # Show last 50 lines`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required if multiple containers)")
	cmd.Flags().BoolVarP(&opts.Follow, "follow", "f", false, "Follow log output")
	cmd.Flags().StringVar(&opts.Tail, "tail", "100", "Number of lines to show from the end (or 'all')")

	return cmd
}

func runLogs(f *cmdutil.Factory, opts *LogsOptions) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("agent", opts.Agent).
		Bool("follow", opts.Follow).
		Str("tail", opts.Tail).
		Msg("streaming logs")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		}
		return err
	}
	defer eng.Close()

	containerMgr := engine.NewContainerManager(eng)

	// Find container
	var containerID string
	var containerName string

	if opts.Agent != "" {
		// Use specific agent
		containerName = engine.ContainerName(cfg.Project, opts.Agent)
		existing, err := eng.FindContainerByName(containerName)
		if err != nil {
			return fmt.Errorf("failed to find container: %w", err)
		}
		if existing == nil {
			fmt.Printf("Error: Container for agent '%s' not found\n\n", opts.Agent)
			fmt.Println("Next Steps:")
			fmt.Println("  1. Run 'claucker ls' to see available containers")
			fmt.Println("  2. Run 'claucker start --agent " + opts.Agent + "' to create it")
			return fmt.Errorf("container not found")
		}
		containerID = existing.ID
	} else {
		// Find containers for project
		containers, err := eng.ListClauckerContainersByProject(cfg.Project, true)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}

		if len(containers) == 0 {
			fmt.Printf("Error: No containers found for project '%s'\n\n", cfg.Project)
			fmt.Println("Next Steps:")
			fmt.Println("  1. Run 'claucker start' to create a container")
			return fmt.Errorf("no containers found")
		}

		if len(containers) > 1 {
			fmt.Printf("Error: Multiple containers found for project '%s'\n\n", cfg.Project)
			fmt.Println("Available agents:")
			for _, c := range containers {
				fmt.Printf("  - %s (%s)\n", c.Agent, c.Status)
			}
			fmt.Println()
			fmt.Println("Use --agent to specify which container:")
			fmt.Printf("  claucker logs --agent %s\n", containers[0].Agent)
			return fmt.Errorf("multiple containers found")
		}

		containerID = containers[0].ID
		containerName = containers[0].Name
	}

	// Get logs
	reader, err := containerMgr.Logs(containerID, opts.Follow, opts.Tail)
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}
	defer reader.Close()

	logger.Debug().Str("container", containerName).Msg("streaming logs")

	// Copy logs to stdout
	_, err = io.Copy(os.Stdout, reader)
	if err != nil && err != context.Canceled {
		return err
	}

	return nil
}
