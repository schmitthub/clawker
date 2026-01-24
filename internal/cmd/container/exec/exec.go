// Package exec provides the container exec command.
package exec

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/output"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/spf13/cobra"
)

// Options holds options for the exec command.
type Options struct {
	Agent       bool // treat first argument as agent name(resolves to clawker.<project>.<agent>)
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
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerName := args[0]
			if opts.Agent {
				var err error
				containerName, err = cmdutil.ResolveContainerName(f, args[0])
				if err != nil {
					return err
				}
			}

			var command []string
			if len(args) > 1 {
				command = args[1:]
			}
			return run(cmd.Context(), f, opts, containerName, command)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Use agent name as first argument (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Keep STDIN open even if not attached")
	cmd.Flags().BoolVarP(&opts.TTY, "tty", "t", false, "Allocate a pseudo-TTY")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Detached mode: run command in the background")
	cmd.Flags().StringArrayVarP(&opts.Env, "env", "e", nil, "Set environment variables")
	cmd.Flags().StringVarP(&opts.Workdir, "workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	cmd.Flags().BoolVar(&opts.Privileged, "privileged", false, "Give extended privileges to the command")

	// Stop parsing flags after the first positional argument (CONTAINER)
	// so that command flags like "sh -c" are passed to the command, not Cobra
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, opts *Options, containerName string, command []string) error {
	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		output.HandleError(err)
		return err
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		output.HandleError(err)
		return err
	}

	// Check if container is running
	if c.State != "running" {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Enable interactive mode early to suppress INFO logs during TTY sessions.
	// This prevents host proxy and other startup logs from interfering with the TUI.
	if !opts.Detach && opts.TTY && opts.Interactive {
		logger.SetInteractiveMode(true)
		defer logger.SetInteractiveMode(false)
	}

	// Create exec configuration
	execConfig := docker.ExecCreateOptions{
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
		output.HandleError(err)
		return err
	}

	execID := execResp.ID
	if execID == "" {
		err := fmt.Errorf("exec ID is empty")
		output.HandleError(err)
		return err
	}

	// If detached, just start and return
	if opts.Detach {
		_, err := client.ExecStart(ctx, execID, docker.ExecStartOptions{
			Detach: true,
			TTY:    opts.TTY,
		})
		if err != nil {
			output.HandleError(err)
			return err
		}
		fmt.Fprintln(f.IOStreams.Out, execID)
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
	attachOpts := docker.ExecAttachOptions{
		TTY: opts.TTY,
	}

	hijacked, err := client.ExecAttach(ctx, execID, attachOpts)
	if err != nil {
		output.HandleError(err)
		return err
	}
	defer hijacked.Close()

	// Handle I/O
	if opts.TTY && pty != nil {
		// Use PTY handler for TTY mode with resize support
		resizeFunc := func(height, width uint) error {
			_, err := client.ExecResize(ctx, execID, docker.ExecResizeOptions{
				Height: height,
				Width:  width,
			})
			return err
		}
		if err := pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc); err != nil {
			return err
		}
		// Check exit code after TTY mode completes
		return checkExecExitCode(ctx, client, execID)
	}

	// Non-TTY mode: demux the multiplexed stream
	outputDone := make(chan error, 1)

	// Copy output using stdcopy to demultiplex stdout/stderr
	go func() {
		_, err := stdcopy.StdCopy(f.IOStreams.Out, f.IOStreams.ErrOut, hijacked.Reader)
		outputDone <- err
	}()

	// Copy stdin to container if interactive
	// This goroutine can finish anytime - we don't wait for it
	if opts.Interactive {
		go func() {
			io.Copy(hijacked.Conn, f.IOStreams.In)
			hijacked.CloseWrite()
		}()
	}

	// Wait for output to complete (stdin finishing early is fine)
	if err := <-outputDone; err != nil && err != io.EOF {
		return err
	}

	// Check exit code
	return checkExecExitCode(ctx, client, execID)
}

// checkExecExitCode inspects the exec and returns an error if exit code is non-zero.
func checkExecExitCode(ctx context.Context, client *docker.Client, execID string) error {
	inspect, err := client.ExecInspect(ctx, execID, docker.ExecInspectOptions{})
	if err != nil {
		// If we can't inspect, don't fail - the command may have completed
		logger.Debug().Err(err).Str("execID", execID).Msg("failed to inspect exec")
		return nil
	}
	if inspect.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d", inspect.ExitCode)
	}
	return nil
}
