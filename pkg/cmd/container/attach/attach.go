// Package attach provides the container attach command.
package attach

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

// Options holds options for the attach command.
type Options struct {
	Agent      string
	NoStdin    bool
	SigProxy   bool
	DetachKeys string
}

// NewCmd creates a new attach command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "attach [OPTIONS] [CONTAINER]",
		Short: "Attach local standard input, output, and error streams to a running container",
		Long: `Attach local standard input, output, and error streams to a running container.

Use ctrl-p, ctrl-q to detach from the container and leave it running.
To stop a container, use clawker container stop.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Attach to a container using agent name
  clawker container attach --agent ralph

  # Attach to a container by full name
  clawker container attach clawker.myapp.ralph

  # Attach without stdin (output only)
  clawker container attach --no-stdin --agent ralph

  # Attach with custom detach keys
  clawker container attach --detach-keys="ctrl-c" --agent ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.AgentArgsValidatorExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVar(&opts.NoStdin, "no-stdin", false, "Do not attach STDIN")
	cmd.Flags().BoolVar(&opts.SigProxy, "sig-proxy", true, "Proxy all received signals to the process")
	cmd.Flags().StringVar(&opts.DetachKeys, "detach-keys", "", "Override the key sequence for detaching a container")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, args []string) error {
	ctx := context.Background()

	// Resolve container name
	containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, args)
	if err != nil {
		return err
	}
	containerName := containers[0]

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

	// Get container info to determine if it has a TTY
	info, err := client.ContainerInspect(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	hasTTY := info.Container.Config.Tty

	// Create attach options
	attachOpts := dockerclient.ContainerAttachOptions{
		Stream: true,
		Stdin:  !opts.NoStdin,
		Stdout: true,
		Stderr: true,
	}

	// Set up TTY if container has one
	var pty *term.PTYHandler
	if hasTTY && !opts.NoStdin {
		pty = term.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container
	hijacked, err := client.ContainerAttach(ctx, c.ID, attachOpts)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer hijacked.Close()

	// Handle I/O
	if hasTTY && pty != nil {
		// Use PTY handler for TTY mode with resize support
		resizeFunc := func(height, width uint) error {
			_, err := client.ContainerResize(ctx, c.ID, height, width)
			return err
		}
		return pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)
	}

	// Non-TTY mode: demux the multiplexed stream
	errCh := make(chan error, 2)
	outputDone := make(chan struct{})

	// Copy output using stdcopy to demultiplex stdout/stderr
	go func() {
		_, err := stdcopy.StdCopy(os.Stdout, os.Stderr, hijacked.Reader)
		if err != nil && err != io.EOF {
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container if enabled
	if !opts.NoStdin {
		go func() {
			_, err := io.Copy(hijacked.Conn, os.Stdin)
			hijacked.CloseWrite()
			if err != nil && err != io.EOF {
				errCh <- err
			}
		}()
	}

	// Wait for output to complete or error
	select {
	case <-outputDone:
		return nil
	case err := <-errCh:
		return err
	}
}
