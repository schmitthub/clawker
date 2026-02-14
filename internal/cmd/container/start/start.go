package start

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/cobra"
)

// StartOptions holds options for the start command.
type StartOptions struct {
	IOStreams     *iostreams.IOStreams
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager

	Agent       bool // Use agent name (resolves to clawker.<project>.<agent>)
	Attach      bool
	Containers  []string
	Interactive bool
}

// NewCmdStart creates the container start command.
func NewCmdStart(f *cmdutil.Factory, runF func(context.Context, *StartOptions) error) *cobra.Command {
	opts := &StartOptions{
		IOStreams:     f.IOStreams,
		Client:       f.Client,
		Config:       f.Config,
		HostProxy:    f.HostProxy,
		SocketBridge: f.SocketBridge,
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
  clawker container start clawker.myapp.dev

  # Start a container using agent name (resolves via project config)
  clawker container start --agent dev

  # Start multiple containers
  clawker container start clawker.myapp.dev clawker.myapp.writer

  # Start and attach to container output
  clawker container start --attach clawker.myapp.dev`,
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
	cfgGateway := opts.Config()
	cfg := cfgGateway.Project

	// --- Phase A: Config + Docker connect + host proxy ---

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Ensure host proxy is running (if enabled)
	if cfg.Security.HostProxyEnabled() {
		hp := opts.HostProxy()
		if hp == nil {
			logger.Debug().Msg("host proxy factory returned nil, skipping")
		} else if err := hp.EnsureRunning(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s Host proxy failed to start. Browser authentication may not work.\n", cs.WarningIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		} else {
			logger.Debug().Msg("host proxy started successfully")
		}
	} else {
		logger.Debug().Msg("host proxy disabled by config")
	}

	// Resolve container names if --agent provided
	containers := opts.Containers
	if opts.Agent {
		resolved, err := docker.ContainerNamesFromAgents(cfgGateway.Resolution.ProjectKey, containers)
		if err != nil {
			return err
		}
		containers = resolved
	}

	// --- Phase B: Start containers ---

	if opts.Attach || opts.Interactive {
		if len(containers) > 1 {
			return fmt.Errorf("you cannot attach to multiple containers at once. If you want to start multiple containers, do so without --attach or --interactive")
		}

		containerName := containers[0]
		return attachAndStart(ctx, ios, client, containerName, opts)
	}

	// Start all containers without attaching
	return startContainersWithoutAttach(ctx, ios, client, containers, opts)
}

// attachAndStart attaches to a container, starts I/O, then starts the container.
// Follows the canonical attach-then-start pattern from run.go:
//
//	Attach → Wait channel → I/O goroutines → Start → Socket bridge → Resize → Wait
//
// I/O streaming starts pre-start; resize starts post-start. This ensures we're
// ready to receive output immediately and avoids kernel pipe buffer issues.
func attachAndStart(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containerName string, opts *StartOptions) error {
	// Find and inspect the container
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

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
	var pty *docker.PTYHandler
	if hasTTY && opts.Interactive {
		pty = docker.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container BEFORE starting it.
	// Critical for short-lived containers where the container might exit
	// before we can attach if we start first.
	logger.Debug().Msg("attaching to container before start")
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		logger.Debug().Err(err).Msg("container attach failed")
		return fmt.Errorf("attaching to container: %w", err)
	}
	defer hijacked.Close()
	logger.Debug().Msg("container attach succeeded")

	// Set up wait channel for container exit following Docker CLI's waitExitOrRemoved pattern.
	// Must use WaitConditionNextExit (not WaitConditionNotRunning) because this is called
	// before the container starts — a "created" container is already not-running.
	logger.Debug().Msg("setting up container wait")
	statusCh := waitForContainerExit(ctx, client, containerID)

	// Start I/O streaming BEFORE starting the container.
	// This ensures we're ready to receive output immediately when the container starts.
	// Following Docker CLI pattern: I/O goroutines start pre-start, resize happens post-start.
	streamDone := make(chan error, 1)

	if hasTTY && pty != nil {
		// TTY mode: use Stream (I/O only, no resize — resize happens after start)
		go func() {
			streamDone <- pty.Stream(ctx, hijacked.HijackedResponse)
		}()
	} else {
		// Non-TTY mode: demux the multiplexed stream
		go func() {
			_, err := stdcopy.StdCopy(ios.Out, ios.ErrOut, hijacked.Reader)
			streamDone <- err
		}()

		// Copy stdin to container if interactive
		if opts.Interactive {
			go func() {
				io.Copy(hijacked.Conn, ios.In)
				hijacked.CloseWrite()
			}()
		}
	}

	// Now start the container — I/O streaming goroutines are already running
	logger.Debug().Msg("starting container")
	_, err = client.ContainerStart(ctx, docker.ContainerStartOptions{
		ContainerID: containerID,
		EnsureNetwork: &docker.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		logger.Debug().Err(err).Msg("container start failed")
		return fmt.Errorf("starting container: %w", err)
	}
	logger.Debug().Msg("container started successfully")

	// Start socket bridge for GPG/SSH forwarding
	cfg := opts.Config().Project
	if shared.NeedsSocketBridge(cfg) && opts.SocketBridge != nil {
		gpgEnabled := cfg.Security.GitCredentials.GPGEnabled()
		if err := opts.SocketBridge().EnsureBridge(containerID, gpgEnabled); err != nil {
			logger.Warn().Err(err).Msg("failed to start socket bridge")
		} else {
			defer opts.SocketBridge().StopBridge(containerID)
		}
	}

	// Set up TTY resize AFTER container is running (Docker CLI's MonitorTtySize pattern).
	// The +1/-1 trick forces a SIGWINCH to trigger TUI redraw on re-attach.
	if hasTTY && pty != nil {
		resizeFunc := func(height, width uint) error {
			_, err := client.ContainerResize(ctx, containerID, height, width)
			return err
		}

		if pty.IsTerminal() {
			width, height, err := pty.GetSize()
			if err != nil {
				logger.Debug().Err(err).Msg("failed to get initial terminal size")
			} else {
				if err := resizeFunc(uint(height+1), uint(width+1)); err != nil {
					logger.Debug().Err(err).Msg("failed to set artificial container TTY size")
				}
				if err := resizeFunc(uint(height), uint(width)); err != nil {
					logger.Debug().Err(err).Msg("failed to set actual container TTY size")
				}
			}

			// Monitor for window resize events (SIGWINCH)
			resizeHandler := signals.NewResizeHandler(resizeFunc, pty.GetSize)
			resizeHandler.Start()
			defer resizeHandler.Stop()
		}
	}

	// Wait for stream completion or container exit.
	// Following Docker CLI's run.go pattern: when stream ends, check exit status;
	// when exit status arrives first, drain the stream.
	select {
	case err := <-streamDone:
		logger.Debug().Err(err).Msg("stream completed")
		if err != nil {
			return err
		}
		// Stream done — check for container exit status.
		// For normal container exits, the status is available almost immediately.
		// For detach (Ctrl+P Ctrl+Q), the container is still running so no status
		// arrives. We use a timeout to distinguish the two cases without blocking
		// forever. This is necessary because we don't do client-side detach key
		// detection (Docker CLI uses term.EscapeError for this).
		select {
		case wr := <-statusCh:
			logger.Debug().Int("exitCode", wr.exitCode).Err(wr.err).Msg("container exited")
			if wr.err != nil {
				return wr.err
			}
			if wr.exitCode != 0 {
				return &cmdutil.ExitError{Code: wr.exitCode}
			}
			return nil
		case <-time.After(2 * time.Second):
			// No exit status within timeout — stream ended due to detach, not exit.
			logger.Debug().Msg("no exit status received after stream ended, assuming detach")
			return nil
		}
	case wr := <-statusCh:
		logger.Debug().Int("exitCode", wr.exitCode).Err(wr.err).Msg("container exited before stream completed")
		if wr.err != nil {
			return wr.err
		}
		if wr.exitCode != 0 {
			return &cmdutil.ExitError{Code: wr.exitCode}
		}
		return nil
	}
}

// waitResult carries the container exit code and any error from the wait.
type waitResult struct {
	exitCode int
	err      error // non-nil if the wait itself failed (distinct from non-zero exit code)
}

// waitForContainerExit wraps ContainerWait into a single result channel.
// Always uses WaitConditionNextExit (start hasn't happened yet, and start
// command never has autoRemove).
func waitForContainerExit(ctx context.Context, client *docker.Client, containerID string) <-chan waitResult {
	ch := make(chan waitResult, 1)
	go func() {
		defer close(ch)
		wr := client.ContainerWait(ctx, containerID, container.WaitConditionNextExit)
		select {
		case <-ctx.Done():
			return
		case result := <-wr.Result:
			if result.Error != nil {
				logger.Error().Str("message", result.Error.Message).Msg("container wait error")
				ch <- waitResult{exitCode: 125, err: fmt.Errorf("container wait error: %s", result.Error.Message)}
			} else {
				ch <- waitResult{exitCode: int(result.StatusCode)}
			}
		case err := <-wr.Error:
			logger.Error().Err(err).Msg("error waiting for container")
			ch <- waitResult{exitCode: 125, err: fmt.Errorf("waiting for container: %w", err)}
		}
	}()
	return ch
}

// startContainersWithoutAttach starts multiple containers without attaching.
func startContainersWithoutAttach(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containers []string, opts *StartOptions) error {
	var errs []error
	for _, name := range containers {
		_, err := client.ContainerStart(ctx, docker.ContainerStartOptions{
			ContainerID: name,
			EnsureNetwork: &docker.EnsureNetworkOptions{
				Name: docker.NetworkName,
			},
		})
		if err != nil {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s Failed to start %s: %v\n", cs.FailureIcon(), name, err)
			errs = append(errs, fmt.Errorf("failed to start %s: %w", name, err))
		} else {
			// Print container name on success
			fmt.Fprintln(ios.Out, name)

			// Start socket bridge for GPG/SSH forwarding (fire-and-forget for detached)
			cfg := opts.Config().Project
			if shared.NeedsSocketBridge(cfg) && opts.SocketBridge != nil {
				gpgEnabled := cfg.Security.GitCredentials.GPGEnabled()
				// Inspect to get full container ID — EnsureBridge must use the same key
				// as exec/run commands (which use the container ID, not name).
				info, inspErr := client.ContainerInspect(ctx, name, docker.ContainerInspectOptions{})
				if inspErr != nil {
					logger.Warn().Err(inspErr).Str("container", name).Msg("failed to inspect container for socket bridge")
				} else if err := opts.SocketBridge().EnsureBridge(info.Container.ID, gpgEnabled); err != nil {
					logger.Warn().Err(err).Str("container", name).Msg("failed to start socket bridge")
				}
			}
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
	}

	return nil
}
