// Package exec provides the container exec command.
package exec

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
)

// ExecOptions holds options for the exec command.
type ExecOptions struct {
	IOStreams     *iostreams.IOStreams
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager

	Agent       bool // treat first argument as agent name(resolves to clawker.<project>.<agent>)
	Interactive bool
	TTY         bool
	Detach      bool
	Env         []string
	Workdir     string
	User        string
	Privileged  bool

	containerName string
	command       []string
}

// NewCmdExec creates a new exec command.
func NewCmdExec(f *cmdutil.Factory, runF func(context.Context, *ExecOptions) error) *cobra.Command {
	opts := &ExecOptions{
		IOStreams:     f.IOStreams,
		Client:       f.Client,
		Config:       f.Config,
		HostProxy:    f.HostProxy,
		SocketBridge: f.SocketBridge,
	}

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
  clawker container exec clawker.myapp.dev ls -la

  # Run a command using agent name (resolves via project config)
  clawker container exec --agent dev ls -la

  # Run an interactive shell
  clawker container exec -it clawker.myapp.dev /bin/bash

  # Run an interactive shell using agent name
  clawker container exec -it --agent dev /bin/bash

  # Run with environment variable
  clawker container exec -e FOO=bar clawker.myapp.dev env

  # Run as a specific user
  clawker container exec -u root clawker.myapp.dev whoami

  # Run in a specific directory
  clawker container exec -w /tmp clawker.myapp.dev pwd`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containerName = args[0]
			if opts.Agent {
				var err error
				opts.containerName, err = docker.ContainerName(opts.Config().Resolution.ProjectKey, args[0])
				if err != nil {
					return err
				}
			}

			if len(args) > 1 {
				opts.command = args[1:]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return execRun(cmd.Context(), opts)
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

func execRun(ctx context.Context, opts *ExecOptions) error {
	ios := opts.IOStreams
	containerName := opts.containerName
	command := opts.command

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}

	// Check if container is running
	if c.State != "running" {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Setup git credential forwarding for exec sessions
	// This enables GPG signing and git credential helpers in exec'd commands
	cfg := opts.Config().Project
	hostProxyRunning := false
	if cfg.Security.HostProxyEnabled() && opts.HostProxy != nil {
		hp := opts.HostProxy()
		if hp == nil {
			logger.Debug().Msg("host proxy function returned nil")
		} else if err := hp.EnsureRunning(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy for exec")
		} else if hp.IsRunning() {
			hostProxyRunning = true
			opts.Env = append(opts.Env, "CLAWKER_HOST_PROXY="+hp.ProxyURL())
			logger.Debug().Str("url", hp.ProxyURL()).Msg("injected host proxy env for exec")
		}
	}

	// Setup git credentials (includes GPG forwarding env vars)
	gitSetup := workspace.SetupGitCredentials(cfg.Security.GitCredentials, hostProxyRunning)
	opts.Env = append(opts.Env, gitSetup.Env...)

	// Ensure socket bridge is running for GPG/SSH forwarding
	// The bridge may already be running from a prior run/start command
	if shared.NeedsSocketBridge(cfg) && opts.SocketBridge != nil {
		gpgEnabled := cfg.Security.GitCredentials.GPGEnabled()
		if err := opts.SocketBridge().EnsureBridge(c.ID, gpgEnabled); err != nil {
			logger.Warn().Err(err).Msg("failed to ensure socket bridge for exec")
		}
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
		return fmt.Errorf("creating exec instance: %w", err)
	}

	execID := execResp.ID
	if execID == "" {
		return fmt.Errorf("exec instance returned empty ID")
	}

	// If detached, just start and return
	if opts.Detach {
		_, err := client.ExecStart(ctx, execID, docker.ExecStartOptions{
			Detach: true,
			TTY:    opts.TTY,
		})
		if err != nil {
			return fmt.Errorf("starting detached exec: %w", err)
		}
		fmt.Fprintln(ios.Out, execID)
		return nil
	}

	// Set up TTY if needed
	var pty *docker.PTYHandler
	if opts.TTY {
		pty = docker.NewPTYHandler()
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
		return fmt.Errorf("attaching to exec: %w", err)
	}
	defer hijacked.Close()

	// Handle I/O
	if opts.TTY && pty != nil {
		// TTY mode: Stream for I/O, separate resize handling
		resizeFunc := func(height, width uint) error {
			_, err := client.ExecResize(ctx, execID, docker.ExecResizeOptions{
				Height: height,
				Width:  width,
			})
			return err
		}

		streamDone := make(chan error, 1)
		go func() {
			streamDone <- pty.Stream(ctx, hijacked.HijackedResponse)
		}()

		// Resize immediately — exec is on a running container
		if pty.IsTerminal() {
			width, height, err := pty.GetSize()
			if err != nil {
				logger.Debug().Err(err).Msg("failed to get initial terminal size")
			} else {
				// +1/-1 trick forces SIGWINCH to trigger TUI redraw
				if err := resizeFunc(uint(height+1), uint(width+1)); err != nil {
					logger.Debug().Err(err).Msg("failed to set artificial exec TTY size")
				}
				if err := resizeFunc(uint(height), uint(width)); err != nil {
					logger.Debug().Err(err).Msg("failed to set actual exec TTY size")
				}
			}

			// Monitor for window resize events (SIGWINCH)
			resizeHandler := signals.NewResizeHandler(resizeFunc, pty.GetSize)
			resizeHandler.Start()
			defer resizeHandler.Stop()
		}

		if err := <-streamDone; err != nil {
			return err
		}
		// Check exit code after TTY mode completes
		return checkExecExitCode(ctx, client, execID)
	}

	// Non-TTY mode: demux the multiplexed stream
	outputDone := make(chan error, 1)

	// Copy output using stdcopy to demultiplex stdout/stderr
	go func() {
		_, err := stdcopy.StdCopy(ios.Out, ios.ErrOut, hijacked.Reader)
		outputDone <- err
	}()

	// Copy stdin to container if interactive
	// This goroutine can finish anytime - we don't wait for it
	if opts.Interactive {
		go func() {
			io.Copy(hijacked.Conn, ios.In)
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
