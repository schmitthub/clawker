package start

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/spf13/cobra"
)

// StartOptions holds options for the start command.
type StartOptions struct {
	IOStreams       *iostreams.IOStreams
	Client          func(context.Context) (*docker.Client, error)
	Config          func() (*config.Config, error)
	Resolution      func() *config.Resolution
	EnsureHostProxy func() error

	Agent       bool // Use agent name (resolves to clawker.<project>.<agent>)
	Attach      bool
	Containers  []string
	Interactive bool
}

// NewCmdStart creates the container start command.
func NewCmdStart(f *cmdutil.Factory, runF func(context.Context, *StartOptions) error) *cobra.Command {
	opts := &StartOptions{
		IOStreams:       f.IOStreams,
		Client:          f.Client,
		Config:          f.Config,
		Resolution:      f.Resolution,
		EnsureHostProxy: f.EnsureHostProxy,
	}

	cmd := &cobra.Command{
		Use:   "start [OPTIONS] CONTAINER [CONTAINER...]",
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
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return startRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Use agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Attach, "attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Attach container's STDIN")

	return cmd
}

func startRun(ctx context.Context, opts *StartOptions) error {
	ctx, cancelFun := context.WithCancel(ctx)
	defer cancelFun()
	ios := opts.IOStreams

	// Load config to check host proxy setting
	cfg, err := opts.Config()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load config, using defaults for host proxy")
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
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
		if err := opts.EnsureHostProxy(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cmdutil.PrintWarning(ios, "Host proxy failed to start. Browser authentication may not work.")
			cmdutil.PrintNextSteps(ios, "To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		} else {
			logger.Debug().Msg("host proxy started successfully")
		}
	} else {
		logger.Debug().Msg("host proxy disabled by config")
	}

	// Resolve container names if --agent provided
	// When opts.Agent is true, all items in opts.Containers are agent names
	containers := opts.Containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Resolution().ProjectKey, containers)
	}

	// If attach or interactive mode, can only work with one container
	if opts.Attach || opts.Interactive {
		if len(containers) > 1 {
			return fmt.Errorf("you cannot attach to multiple containers at once. If you want to start multiple containers, do so without --attach or --interactive")
		}

		containerName := containers[0]
		return attachAndStart(ctx, ios, client, containerName, opts)
	}

	// Start all containers without attaching
	return startContainersWithoutAttach(ctx, ios, client, containers)
}

// attachAndStart attaches to container first, then starts it.
func attachAndStart(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containerName string, opts *StartOptions) error {
	// Find and inspect the container
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// Get container info to determine if it has a TTY
	info, err := client.ContainerInspect(ctx, c.ID, docker.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	hasTTY := info.Container.Config.Tty
	containerID := c.ID

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

	// Attach to container BEFORE starting it
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}
	defer hijacked.Close()

	// Start the container (ensuring it's connected to clawker-net)
	_, err = client.ContainerStart(ctx, docker.ContainerStartOptions{
		ContainerID: containerID,
		EnsureNetwork: &docker.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Set up wait channel for container exit
	waitResult := client.ContainerWait(ctx, containerID, docker.WaitConditionNextExit)

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
			if err != nil {
				return err
			}
			// Stream ended cleanly — check for exit code
			select {
			case result := <-waitResult.Result:
				if result.Error != nil {
					return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
				}
				if result.StatusCode != 0 {
					return &cmdutil.ExitError{Code: int(result.StatusCode)}
				}
				return nil
			case err := <-waitResult.Error:
				return err
			case <-time.After(2 * time.Second):
				// No exit status — container still running (Ctrl+P Ctrl+Q detach)
				return nil
			}
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
			}
			if result.StatusCode != 0 {
				return &cmdutil.ExitError{Code: int(result.StatusCode)}
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
		_, err := stdcopy.StdCopy(ios.Out, ios.ErrOut, hijacked.Reader)
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
		// Output closed - container has exited. Wait for exit status.
		select {
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container %s exit error: %s", containerID[:12], result.Error.Message)
			}
			if result.StatusCode != 0 {
				return &cmdutil.ExitError{Code: int(result.StatusCode)}
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
			return &cmdutil.ExitError{Code: int(result.StatusCode)}
		}
		return nil
	case err := <-waitResult.Error:
		return err
	}
}

// startContainersWithoutAttach starts multiple containers without attaching.
func startContainersWithoutAttach(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containers []string) error {
	var errs []error
	for _, name := range containers {
		_, err := client.ContainerStart(ctx, docker.ContainerStartOptions{
			ContainerID: name,
			EnsureNetwork: &docker.EnsureNetworkOptions{
				Name: docker.NetworkName,
			},
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to start %s: %w", name, err))
			cmdutil.HandleError(ios, err)
		} else {
			// Print container name on success
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d container(s)", len(errs))
	}

	return nil
}
