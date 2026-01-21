package start

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// StartOptions holds options for the start command.
type StartOptions struct {
	Agent       string // Agent name to resolve container
	Attach      bool
	Interactive bool
}

// NewCmdStart creates the container start command.
func NewCmdStart(f *cmdutil.Factory) *cobra.Command {
	opts := &StartOptions{}

	cmd := &cobra.Command{
		Use:   "start [CONTAINER...]",
		Short: "Start one or more stopped containers",
		Long: `Starts one or more stopped clawker containers.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Start a stopped container by full name
  clawker container start clawker.myapp.ralph

  # Start a container using agent name (resolves via project config)
  clawker container start --agent ralph

  # Start multiple containers
  clawker container start clawker.myapp.ralph clawker.myapp.writer

  # Start and attach to container output
  clawker container start --attach clawker.myapp.ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Attach, "attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Attach container's STDIN")

	return cmd
}

func runStart(f *cmdutil.Factory, opts *StartOptions, containers []string) error {
	ctx := context.Background()

	// Load config to check host proxy setting
	cfg, err := f.Config()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load config, using defaults for host proxy")
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Enable interactive mode early to suppress INFO logs during TTY sessions.
	// This prevents host proxy and other startup logs from interfering with the TUI.
	if opts.Attach && opts.Interactive {
		logger.SetInteractiveMode(true)
		defer logger.SetInteractiveMode(false)
	}

	// Ensure host proxy is running for container-to-host communication (if enabled)
	if cfg == nil || cfg.Security.HostProxyEnabled() {
		if err := f.EnsureHostProxy(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cmdutil.PrintWarning("Host proxy failed to start. Browser authentication may not work.")
			cmdutil.PrintNextSteps("To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		}
	}

	// Resolve container names
	containerNames, err := cmdutil.ResolveContainerNames(f, opts.Agent, containers)
	if err != nil {
		return err
	}

	// Attach mode only works with a single container
	if opts.Attach && len(containerNames) > 1 {
		return fmt.Errorf("you cannot attach to multiple containers at once")
	}

	var errs []error
	for _, name := range containerNames {
		if err := startContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			cmdutil.HandleError(err)
		} else if !opts.Attach {
			// Only print container name if not attaching
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d container(s)", len(errs))
	}
	return nil
}

func startContainer(ctx context.Context, client *docker.Client, name string, opts *StartOptions) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return err
	}

	// Start the container (ensuring it's connected to clawker-net)
	_, err = client.ContainerStart(ctx, docker.ContainerStartOptions{
		ContainerID: c.ID,
		EnsureNetwork: &docker.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		return err
	}

	// If attach mode, attach to the container
	if opts.Attach {
		// Re-check container state after start
		c, err = client.FindContainerByName(ctx, name)
		if err != nil {
			return err
		}
		if c.State != "running" {
			return fmt.Errorf("container %q exited immediately after start", name)
		}
		return attachAfterStart(ctx, client, c.ID, opts)
	}

	return nil
}


// attachAfterStart attaches to a container after it has been started.
// For TTY containers with interactive mode, it sets up raw terminal mode with resize support.
// For non-TTY containers, it demultiplexes the Docker stream using stdcopy.
// Returns when the container exits or the output stream closes.
func attachAfterStart(ctx context.Context, client *docker.Client, containerID string, opts *StartOptions) error {
	// Inspect container to check if it has TTY
	info, err := client.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}
	hasTTY := info.Container.Config.Tty

	// Create attach options
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  opts.Interactive,
		Stdout: true,
		Stderr: true,
	}

	// Set up TTY if the container has it and we're interactive
	var pty *term.PTYHandler
	if hasTTY && opts.Interactive {
		pty = term.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		return err
	}
	defer hijacked.Close()

	// Set up wait channel for container exit
	waitResult := client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

	// Handle I/O
	if hasTTY && pty != nil {
		// Use PTY handler for TTY mode with resize support
		resizeFunc := func(height, width uint) error {
			_, err := client.ContainerResize(ctx, containerID, height, width)
			return err
		}

		// Run streaming in a goroutine so we can also wait for container exit
		streamDone := make(chan error, 1)
		go func() {
			streamDone <- pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)
		}()

		// Wait for either stream to end or container to exit
		select {
		case err := <-streamDone:
			return err
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
			}
			if result.StatusCode != 0 {
				return fmt.Errorf("container %s exited with status %d", containerID[:12], result.StatusCode)
			}
			return nil
		case err := <-waitResult.Error:
			return err
		}
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

	// Copy stdin to container if interactive.
	// NOTE: This goroutine is intentionally not awaited - stdin.Read() may block
	// indefinitely, and we exit when output closes or container exits.
	if opts.Interactive {
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
		// Output closed - container has exited. Wait for exit status.
		select {
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
			}
			if result.StatusCode != 0 {
				return fmt.Errorf("container %s exited with status %d", containerID[:12], result.StatusCode)
			}
			return nil
		case err := <-waitResult.Error:
			return err
		}
	case err := <-errCh:
		return err
	case result := <-waitResult.Result:
		// Container exited before output fully read (unusual but possible)
		if result.Error != nil {
			return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
		}
		if result.StatusCode != 0 {
			return fmt.Errorf("container %s exited with status %d", containerID[:12], result.StatusCode)
		}
		return nil
	case err := <-waitResult.Error:
		return err
	}
}
