package sh

import (
	"context"
	"fmt"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// ShOptions contains the options for the sh command.
type ShOptions struct {
	Shell string
	User  string
}

// NewCmdSh creates the sh command.
func NewCmdSh(f *cmdutil.Factory) *cobra.Command {
	opts := &ShOptions{}

	cmd := &cobra.Command{
		Use:   "sh",
		Short: "Open a shell in the running container",
		Long: `Opens an interactive shell session in the running Claude container.

This is useful for debugging, exploring the container filesystem,
or running commands directly without going through Claude.

Examples:
  claucker sh              # Open bash shell
  claucker sh --shell zsh  # Open zsh shell
  claucker sh --user root  # Open shell as root`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSh(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Shell, "shell", "s", "/bin/bash", "Shell to use")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "User to run shell as (default: container default)")

	return cmd
}

func runSh(f *cmdutil.Factory, opts *ShOptions) error {
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
		Str("shell", opts.Shell).
		Msg("opening shell")

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
		fmt.Printf("Error: Container %s is not running\n\n", containerName)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker up' to start the container")
		fmt.Println("  2. Then run 'claucker sh' to open a shell")
		return fmt.Errorf("container not found")
	}

	if existing.State != "running" {
		fmt.Printf("Error: Container %s is not running (state: %s)\n\n", containerName, existing.State)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker up' to start the container")
		return fmt.Errorf("container not running")
	}

	// Setup PTY
	pty := term.NewPTYHandler()
	if err := pty.Setup(); err != nil {
		logger.Warn().Err(err).Msg("failed to setup terminal")
	}
	defer pty.Restore()

	// Execute shell in container
	shellCmd := []string{opts.Shell}
	hijacked, execID, err := containerMgr.Exec(existing.ID, shellCmd, true)
	if err != nil {
		return fmt.Errorf("failed to exec shell: %w", err)
	}

	// Setup resize handler for exec
	if pty.IsTerminal() {
		resizeHandler := term.NewResizeHandler(
			func(height, width uint) error {
				return containerMgr.ResizeExec(execID, height, width)
			},
			pty.GetSize,
		)
		resizeHandler.Start()
		defer resizeHandler.Stop()

		// Initial resize
		resizeHandler.TriggerResize()
	}

	// Stream I/O
	if err := pty.StreamWithResize(ctx, hijacked, func(height, width uint) error {
		return containerMgr.ResizeExec(execID, height, width)
	}); err != nil {
		if err == context.Canceled {
			return nil
		}
		return err
	}

	return nil
}
