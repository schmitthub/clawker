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
	Agent string
	Shell string
	User  string
}

// NewCmdSh creates the sh command.
func NewCmdSh(f *cmdutil.Factory) *cobra.Command {
	opts := &ShOptions{}

	cmd := &cobra.Command{
		Use:   "sh",
		Short: "Open a shell in a running container",
		Long: `Opens an interactive shell session in a running Claude container.

This is useful for debugging, exploring the container filesystem,
or running commands directly without going through Claude.

If multiple containers exist and --agent is not specified, you must specify which agent.

Examples:
  claucker sh                     # Open bash shell (if single container)
  claucker sh --agent ralph       # Open shell in specific agent's container
  claucker sh --shell zsh         # Open zsh shell
  claucker sh --user root         # Open shell as root`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSh(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (required if multiple containers)")
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
		Str("agent", opts.Agent).
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

	containerMgr := engine.NewContainerManager(eng)

	// Find container
	var containerID string
	var containerName string
	var containerState string

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
		containerState = existing.State
	} else {
		// Find running containers for project
		containers, err := eng.ListClauckerContainersByProject(cfg.Project, false) // only running
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}

		if len(containers) == 0 {
			fmt.Printf("Error: No running containers found for project '%s'\n\n", cfg.Project)
			fmt.Println("Next Steps:")
			fmt.Println("  1. Run 'claucker start' to create a container")
			return fmt.Errorf("no containers found")
		}

		if len(containers) > 1 {
			fmt.Printf("Error: Multiple running containers found for project '%s'\n\n", cfg.Project)
			fmt.Println("Available agents:")
			for _, c := range containers {
				fmt.Printf("  - %s\n", c.Agent)
			}
			fmt.Println()
			fmt.Println("Use --agent to specify which container:")
			fmt.Printf("  claucker sh --agent %s\n", containers[0].Agent)
			return fmt.Errorf("multiple containers found")
		}

		containerID = containers[0].ID
		containerName = containers[0].Name
		containerState = containers[0].Status
	}

	if containerState != "running" {
		fmt.Printf("Error: Container %s is not running (state: %s)\n\n", containerName, containerState)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker start' to start the container")
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
	hijacked, execID, err := containerMgr.Exec(containerID, shellCmd, true)
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
