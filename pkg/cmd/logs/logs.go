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
	Follow bool
	Tail   string
}

// NewCmdLogs creates the logs command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	opts := &LogsOptions{}

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream container logs",
		Long: `Shows logs from the Claude container.

Examples:
  claucker logs           # Show recent logs
  claucker logs -f        # Follow log output (like tail -f)
  claucker logs --tail 50 # Show last 50 lines`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(f, opts)
		},
	}

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

	containerName := engine.ContainerName(cfg.Project)
	containerMgr := engine.NewContainerManager(eng)

	// Find container
	existing, err := eng.FindContainerByName(containerName)
	if err != nil {
		return fmt.Errorf("failed to find container: %w", err)
	}

	if existing == nil {
		fmt.Printf("Error: Container %s does not exist\n\n", containerName)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker up' to start the container")
		return fmt.Errorf("container not found")
	}

	// Get logs
	reader, err := containerMgr.Logs(existing.ID, opts.Follow, opts.Tail)
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}
	defer reader.Close()

	// Copy logs to stdout
	_, err = io.Copy(os.Stdout, reader)
	if err != nil && err != context.Canceled {
		return err
	}

	return nil
}
