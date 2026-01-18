// Package exec provides the container exec command.
package exec

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the exec command.
type Options struct {
	Agent       string // Agent name to resolve container
	Interactive bool
	TTY         bool
	Detach      bool
	Env         []string
	Workdir     string
	User        string
	Privileged  bool
}

// NewCmd creates a new exec command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "exec [OPTIONS] [CONTAINER] COMMAND [ARG...]",
		Short: "Execute a command in a running container",
		Long: `Execute a command in a running clawker container.

This creates a new process inside the container and connects to it.
Use -it flags for an interactive shell session.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Run a command
  clawker container exec clawker.myapp.ralph ls -la

  # Run a command using agent name (resolves via project config)
  clawker container exec --agent ralph ls -la

  # Run an interactive shell
  clawker container exec -it clawker.myapp.ralph /bin/bash

  # Run an interactive shell using agent name
  clawker container exec -it --agent ralph /bin/bash

  # Run with environment variable
  clawker container exec -e FOO=bar clawker.myapp.ralph env

  # Run as a specific user
  clawker container exec -u root clawker.myapp.ralph whoami

  # Run in a specific directory
  clawker container exec -w /tmp clawker.myapp.ralph pwd`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: func(cmd *cobra.Command, args []string) error {
			agentFlag, _ := cmd.Flags().GetString("agent")
			if agentFlag != "" {
				// With --agent, only need COMMAND (min 1 arg)
				if len(args) == 0 {
					return fmt.Errorf("requires at least 1 command argument when using --agent")
				}
			} else {
				// Without --agent, need CONTAINER COMMAND (min 2 args)
				if len(args) < 2 {
					return fmt.Errorf("requires at least 2 arg(s), only received %d", len(args))
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var containerName string
			var command []string
			if opts.Agent != "" {
				// Use all args as command
				containerName = "" // Will be resolved from agent
				command = args
			} else {
				// First arg is container, rest are command
				containerName = args[0]
				command = args[1:]
			}
			return run(f, opts, containerName, command)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Keep STDIN open even if not attached")
	cmd.Flags().BoolVarP(&opts.TTY, "tty", "t", false, "Allocate a pseudo-TTY")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Detached mode: run command in the background")
	cmd.Flags().StringArrayVarP(&opts.Env, "env", "e", nil, "Set environment variables")
	cmd.Flags().StringVarP(&opts.Workdir, "workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	cmd.Flags().BoolVar(&opts.Privileged, "privileged", false, "Give extended privileges to the command")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, containerName string, command []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Resolve container name if using --agent
	if opts.Agent != "" {
		cfg, err := f.Config()
		if err != nil {
			cmdutil.PrintError("Failed to load config: %v", err)
			cmdutil.PrintNextSteps(
				"Run 'clawker init' to create a configuration",
				"Or ensure you're in a directory with clawker.yaml",
			)
			return err
		}
		if cfg.Project == "" {
			cmdutil.PrintError("Project name not configured in clawker.yaml")
			cmdutil.PrintNextSteps(
				"Add 'project: <name>' to your clawker.yaml",
			)
			return fmt.Errorf("project name not configured")
		}
		containerName = docker.ContainerName(cfg.Project, opts.Agent)
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Check if container is running
	if c.State != "running" {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Create exec configuration
	execConfig := dockerclient.ExecCreateOptions{
		AttachStdin:  opts.Interactive,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          opts.TTY,
		Cmd:          command,
		Env:          opts.Env,
		WorkingDir:   opts.Workdir,
		User:         opts.User,
		Privileged:   opts.Privileged,
	}

	// Create exec instance
	execResp, err := client.ExecCreate(ctx, c.ID, execConfig)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	execID := execResp.ID
	if execID == "" {
		err := fmt.Errorf("exec ID is empty")
		cmdutil.HandleError(err)
		return err
	}

	// If detached, just start and return
	if opts.Detach {
		_, err := client.ExecStart(ctx, execID, dockerclient.ExecStartOptions{
			Detach: true,
			TTY:    opts.TTY,
		})
		if err != nil {
			cmdutil.HandleError(err)
			return err
		}
		fmt.Println(execID)
		return nil
	}

	// Set up TTY if needed
	var pty *term.PTYHandler
	if opts.TTY {
		pty = term.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to exec
	attachOpts := dockerclient.ExecAttachOptions{
		TTY: opts.TTY,
	}

	hijacked, err := client.ExecAttach(ctx, execID, attachOpts)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer hijacked.Close()

	// Handle I/O
	if opts.TTY && pty != nil {
		// Use PTY handler for TTY mode with resize support
		resizeFunc := func(height, width uint) error {
			_, err := client.ExecResize(ctx, execID, dockerclient.ExecResizeOptions{
				Height: height,
				Width:  width,
			})
			return err
		}
		return pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)
	}

	// Non-TTY mode: demux the multiplexed stream
	errCh := make(chan error, 2)

	// Copy output using stdcopy to demultiplex stdout/stderr
	go func() {
		_, err := stdcopy.StdCopy(os.Stdout, os.Stderr, hijacked.Reader)
		errCh <- err
	}()

	// Copy stdin to container if interactive
	if opts.Interactive {
		go func() {
			_, err := io.Copy(hijacked.Conn, os.Stdin)
			hijacked.CloseWrite()
			errCh <- err
		}()
	}

	// Wait for output to complete
	if err := <-errCh; err != nil && err != io.EOF {
		return err
	}

	return nil
}
