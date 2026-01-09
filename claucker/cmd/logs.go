package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/claucker/claucker/internal/config"
	"github.com/claucker/claucker/internal/engine"
	"github.com/claucker/claucker/internal/term"
	"github.com/claucker/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsTail   string
)

// logsCmd represents the logs command
var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Stream container logs",
	Long: `Shows logs from the Claude container.

Examples:
  claucker logs           # Show recent logs
  claucker logs -f        # Follow log output (like tail -f)
  claucker logs --tail 50 # Show last 50 lines`,
	RunE: runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)

	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().StringVar(&logsTail, "tail", "100", "Number of lines to show from the end (or 'all')")
}

func runLogs(cmd *cobra.Command, args []string) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	// Load configuration
	loader := config.NewLoader(workDir)
	cfg, err := loader.Load()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Bool("follow", logsFollow).
		Str("tail", logsTail).
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
	reader, err := containerMgr.Logs(existing.ID, logsFollow, logsTail)
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
