// Package run provides the container run command.
package run

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	intbuild "github.com/schmitthub/clawker/internal/build"
	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"

	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// RunOptions holds options for the run command.
type RunOptions struct {
	*copts.ContainerOptions

	IOStreams  *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config
	HostProxy func() *hostproxy.Manager
	Prompter  func() *prompter.Prompter
	WorkDir   func() string

	// Run-specific options
	Detach bool

	// Computed fields (set during execution)
	AgentName string

	// Internal (set by RunE before calling runRun)
	flags *pflag.FlagSet
}

// NewCmdRun creates a new container run command.
func NewCmdRun(f *cmdutil.Factory, runF func(context.Context, *RunOptions) error) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &RunOptions{
		ContainerOptions: containerOpts,
		IOStreams:         f.IOStreams,
		Client:           f.Client,
		Config:           f.Config,
		HostProxy:        f.HostProxy,
		Prompter:         f.Prompter,
		WorkDir:          f.WorkDir,
	}

	cmd := &cobra.Command{
		Use:   "run [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create and run a new container",
		Long: `Create and run a new clawker container from the specified image.

Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml.

If IMAGE is "@", clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag`,
		Example: `  # Run an interactive shell
  clawker container run -it --agent shell @ alpine sh

  # Run using default image with generated agent name from config
  clawker container run -it @

  # Run a command
  clawker container run --agent worker @ echo "hello world"
  clawker container run --agent worker myimage:tag echo "hello world"

  # Pass a claude code flag
  clawker container run --detach --agent web @ -p "build entire app, don't make mistakes"

  # Run with environment variables
  clawker container run -it --agent dev -e NODE_ENV=development @ echo $NODE_ENV

  # Run with a bind mount
  clawker container run -it --agent dev -v /host/path:/container/path @

  # Run and automatically remove on exit
  clawker container run --rm -it @ sh`,
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
	copts.AddFlags(cmd.Flags(), containerOpts)
	copts.MarkMutuallyExclusive(cmd)

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
	containerOpts := opts.ContainerOptions
	cfgGateway := opts.Config()

	// Load config for project name
	cfg, err := cfgGateway.Project()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load config: %v", err)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to create a configuration",
			"Or ensure you're in a directory with clawker.yaml",
		)
		return err
	}

	// Load user settings for defaults
	settings, err := cfgGateway.Settings()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load user settings, using defaults")
		settings = config.DefaultSettings()
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Resolve image name
	if containerOpts.Image == "@" {
		resolvedImage, err := docker.ResolveAndValidateImage(ctx, docker.ImageValidationDeps{
			IOStreams: opts.IOStreams,
			Prompter: opts.Prompter,
			SettingsLoader: func() (*config.SettingsLoader, error) {
				return cfgGateway.SettingsLoader()
			},
			DefaultImageTag: intbuild.DefaultImageTag,
			DefaultFlavorOptions: func() []docker.FlavorOption {
				flavors := intbuild.DefaultFlavorOptions()
				out := make([]docker.FlavorOption, len(flavors))
				for i, f := range flavors {
					out[i] = docker.FlavorOption{Name: f.Name, Description: f.Description}
				}
				return out
			},
			BuildDefaultImage: intbuild.BuildDefaultImage,
		}, client, cfg, settings)
		if err != nil {
			// ResolveAndValidateImage already prints appropriate errors
			return err
		}
		if resolvedImage == nil {
			cmdutil.PrintError(ios, "No image specified and no default image configured")
			cmdutil.PrintNextSteps(ios,
				"Specify an image: clawker container run IMAGE",
				"Set default_image in clawker.yaml",
				"Set default_image in ~/.local/clawker/settings.yaml",
				"Build a project image: clawker build",
			)
			return fmt.Errorf("no image specified")
		}
		containerOpts.Image = resolvedImage.Reference
	}

	// Resolve Agent and Container Name
	agentName := containerOpts.GetAgentName()
	if agentName == "" {
		agentName = docker.GenerateRandomName()
	}
	opts.AgentName = agentName
	containerName := docker.ContainerName(cfg.Project, agentName)

	// Setup workspace mounts
	workspaceMounts, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: containerOpts.Mode,
		Config:       cfg,
		AgentName:    agentName,
		WorkDir:      opts.WorkDir(),
	})
	if err != nil {
		return err
	}

	// Enable interactive mode early to suppress INFO logs during TTY sessions.
	// This prevents host proxy and other startup logs from interfering with the TUI.
	if !opts.Detach && containerOpts.TTY && containerOpts.Stdin {
		logger.SetInteractiveMode(true)
		defer logger.SetInteractiveMode(false)
	}

	// Start host proxy server for container-to-host communication (if enabled)
	hostProxyRunning := false
	if cfg.Security.HostProxyEnabled() {
		hp := opts.HostProxy()
		if err := hp.EnsureRunning(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cmdutil.PrintWarning(ios, "Host proxy failed to start. Browser authentication may not work.")
			cmdutil.PrintNextSteps(ios, "To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		} else {
			logger.Debug().Msg("host proxy started successfully")
			hostProxyRunning = true
			// Inject host proxy URL into container environment
			if hp.IsRunning() {
				envVar := "CLAWKER_HOST_PROXY=" + hp.ProxyURL()
				containerOpts.Env = append(containerOpts.Env, envVar)
				logger.Debug().Str("env", envVar).Msg("injected host proxy env var")
			}
		}
	} else {
		logger.Debug().Msg("host proxy disabled by config")
	}

	// Setup git credential forwarding
	gitSetup := workspace.SetupGitCredentials(cfg.Security.GitCredentials, hostProxyRunning)
	workspaceMounts = append(workspaceMounts, gitSetup.Mounts...)
	containerOpts.Env = append(containerOpts.Env, gitSetup.Env...)

	// Inject config-derived runtime env vars (editor, firewall domains, agent env, instruction env)
	runtimeEnv, err := docker.RuntimeEnv(cfg)
	if err != nil {
		return err
	}
	containerOpts.Env = append(containerOpts.Env, runtimeEnv...)

	// Validate cross-field constraints before building configs
	if err := containerOpts.ValidateFlags(); err != nil {
		return err
	}

	// Build configs using shared function
	containerConfig, hostConfig, networkConfig, err := containerOpts.BuildConfigs(opts.flags, workspaceMounts, cfg)
	if err != nil {
		cmdutil.PrintError(ios, "Invalid configuration: %v", err)
		return err
	}

	// Build extra labels for clawker metadata
	extraLabels := map[string]string{
		docker.LabelProject: cfg.Project,
	}
	extraLabels[docker.LabelAgent] = agentName

	// Create container (whail injects managed labels and auto-connects to clawker-net)
	resp, err := client.ContainerCreate(ctx, docker.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkConfig,
		Name:             containerName,
		ExtraLabels:      docker.Labels{extraLabels},
		EnsureNetwork: &docker.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	containerID := resp.ID

	// Print warnings if any
	for _, warning := range resp.Warnings {
		fmt.Fprintln(ios.ErrOut, "Warning:", warning)
	}

	// If detached, just start and print container ID
	if opts.Detach {
		if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
			cmdutil.HandleError(ios, err)
			return err
		}
		fmt.Fprintln(ios.Out, containerID[:12])
		return nil
	}

	// For non-detached mode, attach BEFORE starting to handle short-lived containers
	// and containers with --rm that may exit and be removed before we can attach.
	// See: https://labs.iximiuz.com/tutorials/docker-run-vs-attach-vs-exec
	return attachThenStart(ctx, client, containerID, opts)
}

// attachThenStart attaches to a container BEFORE starting it, then waits for it to exit.
// This ensures we don't miss output from short-lived containers, especially with --rm.
// The sequence follows Docker CLI's approach: attach -> start I/O streaming -> start container -> wait.
// See: https://github.com/docker/cli/blob/master/cli/command/container/run.go
func attachThenStart(ctx context.Context, client *docker.Client, containerID string, opts *RunOptions) error {
	ios := opts.IOStreams
	containerOpts := opts.ContainerOptions

	// Create attach options
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  containerOpts.Stdin,
		Stdout: true,
		Stderr: true,
	}

	// Set up TTY if enabled
	var pty *term.PTYHandler
	if containerOpts.TTY && containerOpts.Stdin {
		pty = term.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container BEFORE starting it
	// This is critical for short-lived containers (especially with --rm) where the container
	// might exit and be removed before we can attach if we start first.
	logger.Debug().Msg("attaching to container before start")
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		logger.Debug().Err(err).Msg("container attach failed")
		cmdutil.HandleError(ios, err)
		return err
	}
	defer hijacked.Close()
	logger.Debug().Msg("container attach succeeded")

	// Set up wait channel for container exit following Docker CLI's waitExitOrRemoved pattern.
	// This wraps the dual-channel ContainerWait into a single status channel.
	// Must use WaitConditionNextExit (not WaitConditionNotRunning) because this is called
	// before the container starts — a "created" container is already not-running.
	logger.Debug().Msg("setting up container wait")
	statusCh := waitForContainerExit(ctx, client, containerID, containerOpts.AutoRemove)

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

	// Now start the container — the I/O streaming goroutines are already running
	logger.Debug().Msg("starting container")
	if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
		logger.Debug().Err(err).Msg("container start failed")
		cmdutil.HandleError(ios, err)
		return err
	}
	logger.Debug().Msg("container started successfully")

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
			resizeHandler := term.NewResizeHandler(resizeFunc, pty.GetSize)
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
		case status := <-statusCh:
			logger.Debug().Int("exitCode", status).Msg("container exited")
			if status != 0 {
				return fmt.Errorf("container exited with status %d", status)
			}
			return nil
		case <-time.After(2 * time.Second):
			// No exit status within timeout — stream ended due to detach, not exit.
			logger.Debug().Msg("no exit status received after stream ended, assuming detach")
			return nil
		}
	case status := <-statusCh:
		logger.Debug().Int("exitCode", status).Msg("container exited before stream completed")
		if status != 0 {
			return fmt.Errorf("container exited with status %d", status)
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
func waitForContainerExit(ctx context.Context, client *docker.Client, containerID string, autoRemove bool) <-chan int {
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
				logger.Error().Str("message", result.Error.Message).Msg("container wait error")
				statusCh <- 125
			} else {
				statusCh <- int(result.StatusCode)
			}
		case err := <-waitResult.Error:
			logger.Error().Err(err).Msg("error waiting for container")
			statusCh <- 125
		}
	}()
	return statusCh
}
