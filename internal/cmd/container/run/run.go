// Package run provides the container run command.
package run

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Options holds options for the run command.
type Options struct {
	*copts.ContainerOptions

	// Run-specific options
	Detach bool // Run in background

	// Internal (resolved from ContainerOptions)
	AgentName string // Resolved container name or id

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmd creates a new container run command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &Options{ContainerOptions: containerOpts}

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
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerOpts.Image = args[0]
			if len(args) > 1 {
				containerOpts.Command = args[1:]
			}
			opts.flags = cmd.Flags()
			return run(cmd.Context(), f, opts)
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

func run(ctx context.Context, f *cmdutil.Factory, opts *Options) error {
	ios := f.IOStreams
	containerOpts := opts.ContainerOptions

	// Load config for project name
	cfg, err := f.Config()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load config: %v", err)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to create a configuration",
			"Or ensure you're in a directory with clawker.yaml",
		)
		return err
	}

	// Load user settings for defaults
	settings, err := f.Settings()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load user settings, using defaults")
		settings = config.DefaultSettings()
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Resolve image name
	if containerOpts.Image == "@" {
		resolvedImage, err := cmdutil.ResolveAndValidateImage(ctx, f, client, cfg, settings)
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
		if err := f.EnsureHostProxy(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cmdutil.PrintWarning(ios, "Host proxy failed to start. Browser authentication may not work.")
			cmdutil.PrintNextSteps(ios, "To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		} else {
			hostProxyRunning = true
			// Inject host proxy URL into container environment
			if envVar := f.HostProxyEnvVar(); envVar != "" {
				containerOpts.Env = append(containerOpts.Env, envVar)
			}
		}
	}

	// Setup git credential forwarding
	gitSetup := workspace.SetupGitCredentials(cfg.Security.GitCredentials, hostProxyRunning)
	workspaceMounts = append(workspaceMounts, gitSetup.Mounts...)
	containerOpts.Env = append(containerOpts.Env, gitSetup.Env...)

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
		fmt.Fprintln(f.IOStreams.ErrOut, "Warning:", warning)
	}

	// If detached, just start and print container ID
	if opts.Detach {
		if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
			cmdutil.HandleError(ios, err)
			return err
		}
		fmt.Fprintln(f.IOStreams.Out, containerID[:12])
		return nil
	}

	// For non-detached mode, attach BEFORE starting to handle short-lived containers
	// and containers with --rm that may exit and be removed before we can attach.
	// See: https://labs.iximiuz.com/tutorials/docker-run-vs-attach-vs-exec
	return attachThenStart(ctx, f, client, containerID, opts)
}

// attachThenStart attaches to a container BEFORE starting it, then waits for it to exit.
// This ensures we don't miss output from short-lived containers, especially with --rm.
// The sequence follows Docker CLI's approach: attach -> start I/O streaming -> start container -> wait.
// See: https://github.com/docker/cli/blob/master/cli/command/container/run.go
func attachThenStart(ctx context.Context, f *cmdutil.Factory, client *docker.Client, containerID string, opts *Options) error {
	ios := f.IOStreams
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
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}
	defer hijacked.Close()

	// Set up wait channel for container exit (use a context that won't be cancelled
	// when we cancel the attach context, following Docker CLI pattern)
	waitResult := client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

	// Start I/O streaming BEFORE starting the container
	// This ensures we're ready to receive output immediately when the container starts
	var outputDone chan error
	var streamDone chan error

	if containerOpts.TTY && pty != nil {
		// TTY mode: use PTY handler for bidirectional streaming with resize support
		resizeFunc := func(height, width uint) error {
			_, err := client.ContainerResize(ctx, containerID, height, width)
			return err
		}
		streamDone = make(chan error, 1)
		go func() {
			streamDone <- pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)
		}()
	} else {
		// Non-TTY mode: demux the multiplexed stream
		outputDone = make(chan error, 1)
		go func() {
			_, err := stdcopy.StdCopy(f.IOStreams.Out, f.IOStreams.ErrOut, hijacked.Reader)
			outputDone <- err
		}()

		// Copy stdin to container if enabled
		if containerOpts.Stdin {
			go func() {
				io.Copy(hijacked.Conn, f.IOStreams.In)
				hijacked.CloseWrite()
			}()
		}
	}

	// Now start the container - the I/O streaming goroutines are already running
	if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Wait for completion based on mode
	if containerOpts.TTY && pty != nil {
		// TTY mode: wait for stream or container exit
		select {
		case err := <-streamDone:
			return err
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container exit error: %s", result.Error.Message)
			}
			if result.StatusCode != 0 {
				return fmt.Errorf("container exited with status %d", result.StatusCode)
			}
			return nil
		case err := <-waitResult.Error:
			return err
		}
	}

	// Non-TTY mode: wait for output to complete or container to exit
	select {
	case err := <-outputDone:
		// Output stream closed, check container status
		if err != nil {
			return err
		}
		select {
		case result := <-waitResult.Result:
			if result.Error != nil {
				return fmt.Errorf("container exit error: %s", result.Error.Message)
			}
			if result.StatusCode != 0 {
				return fmt.Errorf("container exited with status %d", result.StatusCode)
			}
		case err := <-waitResult.Error:
			return err
		default:
		}
		return nil
	case result := <-waitResult.Result:
		if result.Error != nil {
			return fmt.Errorf("container exit error: %s", result.Error.Message)
		}
		if result.StatusCode != 0 {
			return fmt.Errorf("container exited with status %d", result.StatusCode)
		}
		return nil
	case err := <-waitResult.Error:
		return err
	}
}
