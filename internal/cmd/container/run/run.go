// Package run provides the container run command.
package run

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	wtshared "github.com/schmitthub/clawker/internal/cmd/worktree/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// RunOptions holds options for the run command.
type RunOptions struct {
	*shared.ContainerCreateOptions

	IOStreams       *iostreams.IOStreams
	TUI             *tui.TUI
	Client          func(context.Context) (*docker.Client, error)
	Config          func() (config.Config, error)
	ProjectManager  func() (project.ProjectManager, error)
	ProjectRegistry func() (*project.Registry, error)
	HostProxy       func() hostproxy.Service
	ControlPlane    func() manager.Manager
	AdminClient     func(context.Context) (adminv1.AdminServiceClient, error)
	SocketBridge    func() socketbridge.SocketBridgeManager
	Prompter        func() *prompter.Prompter
	Logger          func() (*logger.Logger, error)
	BundleManager   func() (*bundle.Manager, error)
	Version         string

	// Run-specific options
	Detach bool

	// Computed fields (set during execution)
	AgentName string
	Project   string

	// Internal (set by RunE before calling runRun)
	flags *pflag.FlagSet
}

// NewCmdRun creates a new container run command.
func NewCmdRun(f *cmdutil.Factory, runF func(context.Context, *RunOptions) error) *cobra.Command {
	containerOpts := shared.NewContainerOptions()
	opts := &RunOptions{
		ContainerCreateOptions: containerOpts,
		IOStreams:              f.IOStreams,
		TUI:                    f.TUI,
		Client:                 f.Client,
		Config:                 f.Config,
		ProjectManager:         f.ProjectManager,
		ProjectRegistry:        f.ProjectRegistry,
		HostProxy:              f.HostProxy,
		ControlPlane:           f.ControlPlane,
		AdminClient:            f.AdminClient,
		SocketBridge:           f.SocketBridge,
		Prompter:               f.Prompter,
		Logger:                 f.Logger,
		BundleManager:          f.BundleManager,
		Version:                f.Version,
	}

	cmd := &cobra.Command{
		Use:   "run [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create and run a new container",
		Long: `Create and run a new clawker container from the specified image.

Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project is resolved from the current directory.

If IMAGE is "@", clawker resolves the built image for the current scope: the
project image inside a registered project, or the global image (built with
"clawker build" outside any project) elsewhere. "@" selects the default
harness image; "@:<harness>" (e.g. "@:codex") selects a specific harness
image built with "clawker build -t <harness>".`,
		Example: `  # Run an interactive shell
  clawker container run -it --agent ralph @ 

  # Run using default image with generated agent name from config
  clawker container run -it @

  # Pass flags through to the harness
  clawker container run --rm --agent worker @ --help
  clawker container run --rm --agent ralph @ --dangerously-skip-permissions

  # Run in detached mode (background)
  clawker container run --detach --agent web @ -p "build entire app, don't make mistakes" --dangerously-skip-permissions

  # Bypass the harness and run system commands on the container directly
  clawker container run --agent worker @ echo "Hello" 
  clawker container run --agent worker @ zsh 


  # Run with environment variables
  clawker container run -it --agent dev -e NODE_ENV=development @ echo $NODE_ENV

  # Run with a bind mount
  clawker container run -it --agent dev -v /host/path:/container/path @

  # Run and automatically remove on exit
  clawker container run --rm -it @`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerOpts.Image = args[0]
			if len(args) > 1 {
				containerOpts.Command = args[1:]
			}
			opts.flags = cmd.Flags()
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return runRun(cmd.Context(), opts)
		},
	}

	// Add shared container flags
	shared.AddFlags(cmd.Flags(), containerOpts)
	shared.MarkMutuallyExclusive(cmd)
	worktreeComp := wtshared.BranchCompletions(opts.ProjectManager)
	cmd.RegisterFlagCompletionFunc("worktree", worktreeComp) //nolint:errcheck,gosec // errs only on bad wiring

	// Run-specific flags
	// Note: NOT using -d shorthand as it conflicts with global --debug flag
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Run container in background and print container ID")

	// Stop parsing flags after the first positional argument (IMAGE).
	// This allows flags after IMAGE to be passed to the container command.
	// Example: clawker run -it alpine --version
	//   - "-it" are clawker flags (parsed)
	//   - "alpine" is IMAGE
	//   - "--version" is passed to the container (not parsed as clawker flag)
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func runRun(ctx context.Context, opts *RunOptions) error {
	ios := opts.IOStreams
	containerOpts := opts.ContainerCreateOptions

	// Opt-in bundle auto-update before the container resolves its harness/egress
	// floor against the cached bundle set. Warn and proceed.
	cmdutil.RunBundleAutoUpdate(ctx, opts.BundleManager, ios)

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// --- Phase A: Pre-progress (synchronous) ---
	// Config + Docker connect + image resolution — may trigger interactive prompts.

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Resolve project name from ProjectManager (empty if no project registered).
	// Errors are non-fatal because an empty projectName IS the legitimate
	// global-scope-agent case (2-segment naming) — but we log at debug so
	// operators can tell "no project" from "lookup failed" when the
	// composite-identity registry write silently lands against a global-scope
	// row in a project that was supposed to be registered.
	var projectName string
	if opts.ProjectManager != nil {
		log, _ := opts.Logger()
		if log == nil {
			log = logger.Nop()
		}
		pm, pmErr := opts.ProjectManager()
		if pmErr != nil {
			log.Debug().Err(pmErr).Msg("project manager unavailable; announcing as global-scope")
		} else {
			p, pErr := pm.CurrentProject(ctx)
			if pErr != nil {
				log.Debug().Err(pErr).Msg("CurrentProject lookup failed; announcing as global-scope")
			} else {
				projectName = p.Name()
			}
		}
	}

	if harnessTag, isPlaceholder := shared.ParseImagePlaceholder(containerOpts.Image); isPlaceholder {
		ref, resolveErr := shared.ResolvePlaceholderImage(
			ctx, client, cfg, ios, projectName, harnessTag, "run")
		if resolveErr != nil {
			return fmt.Errorf("resolving image: %w", resolveErr)
		}
		containerOpts.Image = ref
	}

	// Warn if workspace mount would include the home directory or higher
	if shared.IsOutsideHome(".") {
		confirmed, promptErr := opts.Prompter().Confirm(
			"WARNING: This will mount your entire home directory (or higher) into a container. Continue?",
			false,
		)
		if promptErr != nil {
			return promptErr
		}
		if !confirmed {
			return cmdutil.SilentError
		}
	}

	type outcome struct {
		result *shared.CreateContainerResult
		err    error
	}
	done := make(chan outcome, 1)

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	go func() {
		r, err := shared.CreateContainer(ctx, &shared.CreateContainerOptions{
			Client:          client,
			Config:          cfg,
			ProjectName:     projectName,
			Options:         containerOpts,
			Flags:           opts.flags,
			Version:         opts.Version,
			ProjectManager:  opts.ProjectManager,
			ProjectRegistry: opts.ProjectRegistry,
			HostProxy:       opts.HostProxy,
			Log:             log,
			Is256Color:      ios.Is256ColorSupported(),
			IsTrueColor:     ios.IsTrueColorSupported(),
		})
		done <- outcome{r, err}
	}()

	ios.StopSpinner()

	o := <-done
	if o.err != nil {
		return o.err
	}

	opts.AgentName = o.result.AgentName
	opts.Project = projectName

	// Bootstrap host services (CP ensure, host proxy, firewall init/rules)
	// under a spinner BEFORE attach. Doing it here — in cooked mode, before
	// pty.Setup hijacks the terminal — keeps the spinner clear of the raw-tty
	// stream and guarantees the host stops writing to ios.ErrOut before
	// clawkerd starts writing to the attached TTY (pty.Stream copies hijacked
	// container output to os.Stdout). Both detach and attach paths share the
	// same pre-start, so the bootstrap effort isn't repeated downstream.
	cmdOpts := shared.CommandOpts{
		Client:       opts.Client,
		Config:       opts.Config,
		HostProxy:    opts.HostProxy,
		ControlPlane: opts.ControlPlane,
		AdminClient:  opts.AdminClient,
		SocketBridge: opts.SocketBridge,
		Logger:       opts.Logger,
		AgentName:    opts.AgentName,
		Project:      opts.Project,
	}
	if err := ios.RunWithSpinner("Bootstrapping host services", func() error {
		return shared.BootstrapServicesPreStart(ctx, o.result.ContainerID, cmdOpts)
	}); err != nil {
		// Reap-on-failed-start: this invocation just created the container —
		// free its name so the same command can simply be re-run.
		//nolint:contextcheck,wrapcheck // reap runs on context.Background (Ctrl+C must not abort it) and returns the already-wrapped caller error
		return shared.ReapFailedStart(
			client,
			o.result.ContainerID,
			fmt.Errorf("pre-start bootstrapping failed: %w", err),
		)
	}

	if opts.Detach {
		// Pre-start already ran; just docker start + post-start (eBPF attach +
		// socket bridge). No spinner — detach output is the container ID.
		//nolint:exhaustruct // start options: unset fields are intentional defaults; the moby embed is unnameable outside whail
		if _, startErr := client.ContainerStart(
			ctx,
			docker.ContainerStartOptions{ContainerID: o.result.ContainerID},
		); startErr != nil {
			//nolint:contextcheck,wrapcheck // reap runs on context.Background (Ctrl+C must not abort it) and returns the already-wrapped caller error
			return shared.ReapFailedStart(client, o.result.ContainerID, fmt.Errorf("starting container: %w", startErr))
		}
		if err := shared.BootstrapServicesPostStart(ctx, o.result.ContainerID, cmdOpts); err != nil {
			return fmt.Errorf("starting container: %w", err)
		}

		fmt.Fprintln(ios.Out, o.result.ContainerID[:12])
		return nil
	}

	return attachThenStart(ctx, client, o.result.ContainerID, cmdOpts, opts, log)
}

// attachThenStart attaches to a container BEFORE starting it, then waits for it to exit.
// This ensures we don't miss output from short-lived containers, especially with --rm.
// The sequence follows Docker CLI's approach: attach -> start I/O streaming -> start container -> wait.
// See: https://github.com/docker/cli/blob/master/cli/command/container/run.go
//
// cmdOpts carries the already-resolved CommandOpts so docker start + post-start
// can fire without re-deriving providers. Pre-start has already run in cooked
// mode at the call site (runRun) — DO NOT re-invoke it here.
//
//nolint:gocognit,cyclop,funlen // delicate attach→stream→start→wait sequence; the ordering invariants read better linear than split
func attachThenStart(
	ctx context.Context,
	client *docker.Client,
	containerID string,
	cmdOpts shared.CommandOpts,
	opts *RunOptions,
	log *logger.Logger,
) error {
	ios := opts.IOStreams
	containerOpts := opts.ContainerCreateOptions

	// Create attach options
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  containerOpts.Stdin,
		Stdout: true,
		Stderr: true,
	}

	// Set up TTY if enabled
	var pty *docker.PTYHandler
	if containerOpts.TTY && containerOpts.Stdin {
		pty = docker.NewPTYHandler(log)
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container BEFORE starting it
	// This is critical for short-lived containers (especially with --rm) where the container
	// might exit and be removed before we can attach if we start first.
	log.Debug().Msg("attaching to container before start")
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		log.Debug().Err(err).Msg("container attach failed")
		return fmt.Errorf("attaching to container: %w", err)
	}
	defer hijacked.Close()
	log.Debug().Msg("container attach succeeded")

	// Set up wait channel for container exit following Docker CLI's waitExitOrRemoved pattern.
	// This wraps the dual-channel ContainerWait into a single status channel.
	// Must use WaitConditionNextExit (not WaitConditionNotRunning) because this is called
	// before the container starts — a "created" container is already not-running.
	log.Debug().Msg("setting up container wait")
	statusCh := waitForContainerExit(ctx, client, containerID, containerOpts.AutoRemove, log)

	// Start I/O streaming BEFORE starting the container.
	// This ensures we're ready to receive output immediately when the container starts.
	// Following Docker CLI pattern: I/O goroutines start pre-start, resize happens post-start.
	streamDone := make(chan error, 1)

	if containerOpts.TTY && pty != nil {
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

		// Copy stdin to container if enabled
		if containerOpts.Stdin {
			go func() {
				io.Copy(hijacked.Conn, ios.In)
				hijacked.CloseWrite()
			}()
		}
	}

	// Docker start + post-start run silently — clawkerd boots immediately and
	// owns the foreground TTY from here on, writing init progress lines to
	// os.Stdout via pty.Stream. Any host-side output (spinner, log line,
	// prompt) here would interleave with clawkerd's lines on the same
	// terminal; keep this silent.
	log.Debug().Msg("starting container")
	if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
		log.Debug().Err(err).Msg("container start failed")
		return shared.ReapFailedStart(client, containerID, fmt.Errorf("starting container: %w", err))
	}
	if err := shared.BootstrapServicesPostStart(ctx, containerID, cmdOpts); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	log.Debug().Msg("container started successfully")

	// Set up TTY resize AFTER container is running (Docker CLI's MonitorTtySize pattern).
	// The +1/-1 trick forces a SIGWINCH to trigger TUI redraw on re-attach.
	if containerOpts.TTY && pty != nil {
		resizeFunc := func(height, width uint) error {
			_, err := client.ContainerResize(ctx, containerID, height, width)
			return err
		}

		if pty.IsTerminal() {
			width, height, err := pty.GetSize()
			if err != nil {
				log.Debug().Err(err).Msg("failed to get initial terminal size")
			} else {
				if err := resizeFunc(uint(height+1), uint(width+1)); err != nil {
					log.Debug().Err(err).Msg("failed to set artificial container TTY size")
				}
				if err := resizeFunc(uint(height), uint(width)); err != nil {
					log.Debug().Err(err).Msg("failed to set actual container TTY size")
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
		log.Debug().Err(err).Msg("stream completed")
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
		case status := <-statusCh:
			log.Debug().Int("exitCode", status).Msg("container exited")
			if status != 0 {
				return &cmdutil.ExitError{Code: status}
			}
			return nil
		case <-time.After(2 * time.Second):
			// No exit status within timeout — stream ended due to detach, not exit.
			log.Debug().Msg("no exit status received after stream ended, assuming detach")
			return nil
		}
	case status := <-statusCh:
		log.Debug().Int("exitCode", status).Msg("container exited before stream completed")
		// Wait for stream to finish flushing buffered output.
		// Docker CLI does the same: "we need to keep the streamer running
		// until all output is read." The daemon closes the hijacked connection
		// on container exit, so the stream goroutine terminates via EOF.
		<-streamDone
		if status != 0 {
			return &cmdutil.ExitError{Code: status}
		}
		return nil
	}
}

// waitForContainerExit sets up a channel that receives the container's exit status code.
// It follows Docker CLI's waitExitOrRemoved pattern:
//   - Uses WaitConditionNextExit (not WaitConditionNotRunning) so it can be called
//     BEFORE the container starts without returning immediately for "created" containers.
//   - Uses WaitConditionRemoved when autoRemove is true (--rm) so the wait doesn't fail
//     when the container is removed after exit.
func waitForContainerExit(
	ctx context.Context,
	client *docker.Client,
	containerID string,
	autoRemove bool,
	log *logger.Logger,
) <-chan int {
	condition := container.WaitConditionNextExit
	if autoRemove {
		condition = container.WaitConditionRemoved
	}

	statusCh := make(chan int, 1)
	go func() {
		defer close(statusCh)
		waitResult := client.ContainerWait(ctx, containerID, condition)
		select {
		case <-ctx.Done():
			return
		case result := <-waitResult.Result:
			if result.Error != nil {
				log.Error().Str("message", result.Error.Message).Msg("container wait error")
				statusCh <- 125
			} else {
				statusCh <- int(result.StatusCode)
			}
		case err := <-waitResult.Error:
			log.Error().Err(err).Msg("error waiting for container")
			statusCh <- 125
		}
	}()
	return statusCh
}
