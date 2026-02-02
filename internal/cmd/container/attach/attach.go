// Package attach provides the container attach command.
package attach

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/spf13/cobra"
)

// AttachOptions holds options for the attach command.
type AttachOptions struct {
	IOStreams  *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config
	HostProxy func() *hostproxy.Manager

	Agent      bool // treat argument as agent name(resolves to clawker.<project>.<agent>)
	NoStdin    bool
	SigProxy   bool
	DetachKeys string
	container  string
}

// NewCmdAttach creates a new attach command.
func NewCmdAttach(f *cmdutil.Factory, runF func(context.Context, *AttachOptions) error) *cobra.Command {
	opts := &AttachOptions{
		IOStreams:  f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
		HostProxy: f.HostProxy,
	}

	cmd := &cobra.Command{
		Use:   "attach [OPTIONS] CONTAINER",
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
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.container = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return attachRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat argument as agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVar(&opts.NoStdin, "no-stdin", false, "Do not attach STDIN")
	cmd.Flags().BoolVar(&opts.SigProxy, "sig-proxy", true, "Proxy all received signals to the process")
	cmd.Flags().StringVar(&opts.DetachKeys, "detach-keys", "", "Override the key sequence for detaching a container")

	return cmd
}

func attachRun(ctx context.Context, opts *AttachOptions) error {
	ios := opts.IOStreams

	container := opts.container
	if opts.Agent {
		container = docker.ContainerName(opts.Config().Resolution().ProjectKey, container)
	}
	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, container)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", container, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", container)
	}

	// Check if container is running
	if c.State != "running" {
		return fmt.Errorf("container %q is not running", container)
	}

	// Start host proxy so browser opening and other host actions work
	if opts.HostProxy != nil {
		hp := opts.HostProxy()
		if err := hp.EnsureRunning(); err != nil {
			return fmt.Errorf("failed to start host proxy: %w", err)
		}
	}

	// Get container info to determine if it has a TTY
	info, err := client.ContainerInspect(ctx, c.ID, docker.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	hasTTY := info.Container.Config.Tty

	// Create attach options
	attachOpts := docker.ContainerAttachOptions{
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
		cmdutil.HandleError(ios, err)
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
		_, err := stdcopy.StdCopy(ios.Out, ios.ErrOut, hijacked.Reader)
		if err != nil && err != io.EOF {
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container if enabled
	if !opts.NoStdin {
		go func() {
			_, err := io.Copy(hijacked.Conn, ios.In)
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
