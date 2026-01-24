// Package run provides the container run command.
package run

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/output"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/spf13/cobra"
)

// Options holds options for the run command.
type Options struct {
	// Naming
	Agent     string // Use an agent name for first argument, image is auto resolved
	Name      string // Full container name (overrides agent)
	AgentName string // Resolved container name or id

	// Container configuration
	Env        []string // Environment variables
	Volumes    []string // Bind mounts
	Publish    []string // Port mappings
	Workdir    string   // Working directory
	User       string   // User
	Entrypoint string   // Override entrypoint
	TTY        bool     // Allocate TTY
	Stdin      bool     // Keep STDIN open
	Network    string   // Network connection
	Labels     []string // Additional labels
	AutoRemove bool     // Auto-remove on exit

	// Run-specific options
	Detach bool   // Run in background
	Mode   string // Workspace mode: "bind" or "snapshot" (empty = use config default)

	// Internal (set after parsing positional args)
	Image   string
	Command []string
}

// NewCmd creates a new container run command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

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
			opts.Image = args[0]
			if len(args) > 1 {
				opts.Command = args[1:]
			}
			return run(cmd.Context(), f, opts)
		},
	}

	flags := cmd.Flags()
	flags.SetInterspersed(false)

	// Naming flags
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Assign a name to the agent, used in container name (mutually exclusive with --name)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Same as --agent; provided for Docker CLI familiarity (mutually exclusive with --agent)")

	// Container configuration flags
	cmd.Flags().StringArrayVarP(&opts.Env, "env", "e", nil, "Set environment variables")
	cmd.Flags().StringArrayVarP(&opts.Volumes, "volume", "v", nil, "Bind mount a volume")
	cmd.Flags().StringArrayVarP(&opts.Publish, "publish", "p", nil, "Publish container port(s) to host")
	cmd.Flags().StringVarP(&opts.Workdir, "workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "Username or UID")
	cmd.Flags().StringVar(&opts.Entrypoint, "entrypoint", "", "Overwrite the default ENTRYPOINT")
	cmd.Flags().BoolVarP(&opts.TTY, "tty", "t", false, "Allocate a pseudo-TTY")
	cmd.Flags().BoolVarP(&opts.Stdin, "interactive", "i", false, "Keep STDIN open even if not attached")
	cmd.Flags().StringVar(&opts.Network, "network", "", "Connect container to a network")
	cmd.Flags().StringArrayVarP(&opts.Labels, "label", "l", nil, "Set metadata on container")
	cmd.Flags().BoolVar(&opts.AutoRemove, "rm", false, "Automatically remove container when it exits")

	// Run-specific flags
	// Note: NOT using -d shorthand as it conflicts with global --debug flag
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Run container in background and print container ID")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)")

	cmd.MarkFlagsMutuallyExclusive("agent", "name")

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

	// Load config for project name
	cfg, err := f.Config()
	if err != nil {
		output.PrintError("Failed to load config: %v", err)
		output.PrintNextSteps(
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
		output.HandleError(err)
		return err
	}

	// Resolve image name
	if opts.Image == "@" {
		resolvedImage, err := cmdutil.ResolveAndValidateImage(ctx, f, client, cfg, settings)
		if err != nil {
			// ResolveAndValidateImage already prints appropriate errors
			return err
		}
		if resolvedImage == nil {
			output.PrintError("No image specified and no default image configured")
			output.PrintNextSteps(
				"Specify an image: clawker container run IMAGE",
				"Set default_image in clawker.yaml",
				"Set default_image in ~/.local/clawker/settings.yaml",
				"Build a project image: clawker build",
			)
			return fmt.Errorf("no image specified")
		}
		opts.Image = resolvedImage.Reference
	}

	// Resolve Agent and Container Name

	// If agent or name set, set AgentName
	opts.AgentName = opts.Agent
	if opts.AgentName == "" && opts.Name != "" {
		opts.AgentName = opts.Name
	}
	var containerName string
	var agent string
	if opts.AgentName == "" {
		agent = docker.GenerateRandomName()
	} else {
		agent = opts.AgentName
	}
	containerName = docker.ContainerName(cfg.Project, agent)

	// Setup workspace mounts
	workspaceMounts, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: opts.Mode,
		Config:       cfg,
		AgentName:    agent,
	})
	if err != nil {
		return err
	}

	// Enable interactive mode early to suppress INFO logs during TTY sessions.
	// This prevents host proxy and other startup logs from interfering with the TUI.
	if !opts.Detach && opts.TTY && opts.Stdin {
		logger.SetInteractiveMode(true)
		defer logger.SetInteractiveMode(false)
	}

	// Start host proxy server for container-to-host communication (if enabled)
	hostProxyRunning := false
	if cfg.Security.HostProxyEnabled() {
		if err := f.EnsureHostProxy(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			output.PrintWarning("Host proxy failed to start. Browser authentication may not work.")
			output.PrintNextSteps("To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
		} else {
			hostProxyRunning = true
			// Inject host proxy URL into container environment
			if envVar := f.HostProxyEnvVar(); envVar != "" {
				opts.Env = append(opts.Env, envVar)
			}
		}
	}

	// Setup git credential forwarding
	gitSetup := workspace.SetupGitCredentials(cfg.Security.GitCredentials, hostProxyRunning)
	workspaceMounts = append(workspaceMounts, gitSetup.Mounts...)
	opts.Env = append(opts.Env, gitSetup.Env...)

	// Build configs
	containerConfig, hostConfig, networkConfig, err := buildConfigs(opts, workspaceMounts, cfg)
	if err != nil {
		output.PrintError("Invalid configuration: %v", err)
		return err
	}

	// Build extra labels for clawker metadata
	extraLabels := map[string]string{
		docker.LabelProject: cfg.Project,
	}
	if agent != "" {
		extraLabels[docker.LabelAgent] = agent
	}

	// Create container (whail injects managed labels and auto-connects to clawker-net)
	resp, err := client.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkConfig,
		Name:             containerName,
		ExtraLabels:      whail.Labels{extraLabels},
		EnsureNetwork: &whail.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		output.HandleError(err)
		return err
	}

	containerID := resp.ID

	// Print warnings if any
	for _, warning := range resp.Warnings {
		fmt.Fprintln(f.IOStreams.ErrOut, "Warning:", warning)
	}

	// Start the container
	if _, err := client.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: containerID}); err != nil {
		output.HandleError(err)
		return err
	}

	// If detached, print container ID and exit
	if opts.Detach {
		fmt.Fprintln(f.IOStreams.Out, containerID[:12])
		return nil
	}

	// Attach to container
	return attachAndWait(ctx, f, client, containerID, opts)
}

// attachAndWait attaches to a running container and waits for it to exit.
func attachAndWait(ctx context.Context, f *cmdutil.Factory, client *docker.Client, containerID string, opts *Options) error {
	// Create attach options
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  opts.Stdin,
		Stdout: true,
		Stderr: true,
	}

	// Set up TTY if enabled
	var pty *term.PTYHandler
	if opts.TTY && opts.Stdin {
		pty = term.NewPTYHandler()
		if err := pty.Setup(); err != nil {
			return fmt.Errorf("failed to set up terminal: %w", err)
		}
		defer pty.Restore()
	}

	// Attach to container
	hijacked, err := client.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		output.HandleError(err)
		return err
	}
	defer hijacked.Close()

	// Set up wait channel for container exit
	waitResult := client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

	// Handle I/O
	if opts.TTY && pty != nil {
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

	// Non-TTY mode: demux the multiplexed stream
	outputDone := make(chan struct{})

	// Copy output using stdcopy to demultiplex stdout/stderr
	go func() {
		stdcopy.StdCopy(f.IOStreams.Out, f.IOStreams.ErrOut, hijacked.Reader)
		close(outputDone)
	}()

	// Copy stdin to container if enabled
	if opts.Stdin {
		go func() {
			io.Copy(hijacked.Conn, f.IOStreams.In)
			hijacked.CloseWrite()
		}()
	}

	// Wait for container to exit
	select {
	case <-outputDone:
		// Output closed, check container status
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

// buildConfigs builds Docker container, host, and network configurations from options.
func buildConfigs(opts *Options, mounts []mount.Mount, projectCfg *config.Config) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	// Container config
	cfg := &container.Config{
		Image:        opts.Image,
		Tty:          opts.TTY,
		OpenStdin:    opts.Stdin,
		AttachStdin:  opts.Stdin,
		AttachStdout: true,
		AttachStderr: true,
		Env:          opts.Env,
		WorkingDir:   opts.Workdir,
		User:         opts.User,
	}

	// Set command if provided
	if len(opts.Command) > 0 {
		cfg.Cmd = opts.Command
	}

	// Set entrypoint if provided
	if opts.Entrypoint != "" {
		cfg.Entrypoint = []string{opts.Entrypoint}
	}

	// Parse additional labels
	if len(opts.Labels) > 0 {
		cfg.Labels = make(map[string]string)
		for _, l := range opts.Labels {
			parts := strings.SplitN(l, "=", 2)
			if len(parts) == 2 {
				cfg.Labels[parts[0]] = parts[1]
			} else {
				cfg.Labels[parts[0]] = ""
			}
		}
	}

	// Host config
	hostCfg := &container.HostConfig{
		AutoRemove: opts.AutoRemove,
		Mounts:     mounts,
		CapAdd:     projectCfg.Security.CapAdd,
	}

	// Parse user-provided volumes (via -v flag) as Binds
	if len(opts.Volumes) > 0 {
		hostCfg.Binds = opts.Volumes
	}

	// Parse port mappings
	if len(opts.Publish) > 0 {
		exposedPorts, portBindings, err := parsePortMappings(opts.Publish)
		if err != nil {
			return nil, nil, nil, err
		}
		cfg.ExposedPorts = exposedPorts
		hostCfg.PortBindings = portBindings
	}

	// Network config
	var networkCfg *network.NetworkingConfig
	if opts.Network != "" {
		networkCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				opts.Network: {},
			},
		}
	}

	return cfg, hostCfg, networkCfg, nil
}

// parsePortMappings converts port mapping specs (e.g., "8080:80/tcp") to network types.
func parsePortMappings(specs []string) (network.PortSet, network.PortMap, error) {
	exposedPorts := make(network.PortSet)
	portBindings := make(network.PortMap)

	for _, spec := range specs {
		portMappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid port mapping %q: %w", spec, err)
		}
		for _, pm := range portMappings {
			// Convert nat.Port to network.Port
			// nat.Port is a string like "80/tcp"
			netPort, err := network.ParsePort(string(pm.Port))
			if err != nil {
				return nil, nil, fmt.Errorf("invalid port %q: %w", pm.Port, err)
			}

			exposedPorts[netPort] = struct{}{}

			// Convert nat.PortBinding to network.PortBinding
			// HostIP needs to be netip.Addr; HostPort stays as string
			var hostIP netip.Addr
			if pm.Binding.HostIP != "" {
				hostIP, err = netip.ParseAddr(pm.Binding.HostIP)
				if err != nil {
					return nil, nil, fmt.Errorf("invalid host IP %q: %w", pm.Binding.HostIP, err)
				}
			}
			binding := network.PortBinding{
				HostIP:   hostIP,
				HostPort: pm.Binding.HostPort,
			}
			portBindings[netPort] = append(portBindings[netPort], binding)
		}
	}

	return exposedPorts, portBindings, nil
}
