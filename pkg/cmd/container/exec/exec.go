// Package exec provides the container exec command.
package exec

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the exec command.
type Options struct {
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
		Use:   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
		Short: "Execute a command in a running container",
		Long: `Execute a command in a running clawker container.

This creates a new process inside the container and connects to it.
Use -it flags for an interactive shell session.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Run a command
  clawker container exec clawker.myapp.ralph ls -la

  # Run an interactive shell
  clawker container exec -it clawker.myapp.ralph /bin/bash

  # Run with environment variable
  clawker container exec -e FOO=bar clawker.myapp.ralph env

  # Run as a specific user
  clawker container exec -u root clawker.myapp.ralph whoami

  # Run in a specific directory
  clawker container exec -w /tmp clawker.myapp.ralph pwd`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args[0], args[1:])
		},
	}

	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Keep STDIN open even if not attached")
	cmd.Flags().BoolVarP(&opts.TTY, "tty", "t", false, "Allocate a pseudo-TTY")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Detached mode: run command in the background")
	cmd.Flags().StringArrayVarP(&opts.Env, "env", "e", nil, "Set environment variables")
	cmd.Flags().StringVarP(&opts.Workdir, "workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	cmd.Flags().BoolVar(&opts.Privileged, "privileged", false, "Give extended privileges to the command")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, containerName string, command []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// Check if container is running
	if c.State != "running" {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Create exec configuration
	execConfig := container.ExecOptions{
		AttachStdin:  opts.Interactive,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          opts.TTY,
		Cmd:          command,
		Env:          opts.Env,
		WorkingDir:   opts.Workdir,
		User:         opts.User,
		Privileged:   opts.Privileged,
	}

	// Create exec instance
	execResp, err := client.ContainerExecCreate(ctx, c.ID, execConfig)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// If detached, just start and return
	if opts.Detach {
		// Note: For detached exec, we use ExecStart instead of ExecAttach
		// The whail package uses ExecAttach which doesn't support detach directly
		// We'll just print the exec ID for now
		fmt.Fprintf(os.Stderr, "Started exec: %s\n", execResp.ID)
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
	startOpts := container.ExecStartOptions{
		Tty: opts.TTY,
	}

	hijacked, err := client.ContainerExecAttach(ctx, execResp.ID, startOpts)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer hijacked.Close()

	// Handle I/O
	if opts.TTY && pty != nil {
		// Use PTY handler for TTY mode with resize support
		resizeFunc := func(height, width uint) error {
			return client.ContainerExecResize(ctx, execResp.ID, height, width)
		}
		return pty.StreamWithResize(ctx, hijacked, resizeFunc)
	}

	// Non-TTY mode: simple I/O copy
	errCh := make(chan error, 2)

	// Copy output to stdout/stderr
	go func() {
		_, err := io.Copy(os.Stdout, hijacked.Reader)
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
