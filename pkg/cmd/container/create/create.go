// Package create provides the container create command.
package create

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/strslice"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/spf13/cobra"
)

// Options holds options for the create command.
type Options struct {
	// Naming
	Agent string // Agent name for clawker naming (mutually exclusive with Name)
	Name  string // Full container name (overrides agent)

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

	// Workspace mode
	Mode string // Workspace mode: "bind" or "snapshot" (empty = use config default)

	// Internal (set after parsing positional args)
	Image   string
	Command []string
}

// NewCmd creates a new container create command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] [IMAGE] [COMMAND] [ARG...]",
		Short: "Create a new container",
		Long: `Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml. When --name is provided, it overrides this.

If IMAGE is not specified, clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag`,
		Example: `  # Create a container with a specific agent name
  clawker container create --agent myagent alpine

  # Create a container using default image from config
  clawker container create --agent myagent

  # Create a container with a command
  clawker container create --agent worker alpine echo "hello world"

  # Create a container with environment variables and ports
  clawker container create --agent web -e PORT=8080 -p 8080:8080 node:20

  # Create a container with a bind mount
  clawker container create --agent dev -v /host/path:/container/path alpine

  # Create an interactive container with TTY
  clawker container create -it --agent shell alpine sh`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				// Determine if first arg is an image name or a container command/flag.
				// - With IMAGE: clawker create alpine --version (args[0]="alpine", args[1:]="--version")
				// - With -- separator: clawker create --agent x -- --flag (args[0]="--flag", starts with "-")
				// When args[0] starts with "-", treat all args as container command; resolve image from defaults.
				if strings.HasPrefix(args[0], "-") {
					opts.Command = args
				} else {
					opts.Image = args[0]
					if len(args) > 1 {
						opts.Command = args[1:]
					}
				}
			}
			return run(f, opts)
		},
	}

	// Naming flags
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name for container (uses clawker.<project>.<agent> naming)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Full container name (overrides --agent)")

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
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)")

	cmd.MarkFlagsMutuallyExclusive("agent", "name")

	// Stop parsing flags after the first positional argument (IMAGE).
	// This allows flags after IMAGE to be passed to the container command.
	// Example: clawker create alpine --version
	//   - "alpine" is IMAGE
	//   - "--version" is passed to the container (not parsed as clawker flag)
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func run(f *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()

	// Load config for project name
	cfg, err := f.Config()
	if err != nil {
		cmdutil.PrintError("Failed to load config: %v", err)
		cmdutil.PrintNextSteps(
			"Run 'clawker init' to create a configuration",
			"Or ensure you're in a directory with clawker.yaml",
		)
		return err
	}

	// Load settings for image resolution
	settings, err := f.Settings()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load user settings, using defaults")
	}
	if settings == nil {
		settings = config.DefaultSettings()
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Resolve and validate image
	resolvedImage, err := cmdutil.ResolveAndValidateImage(ctx, f, client, cfg, settings, cmdutil.ResolveAndValidateImageOptions{
		ExplicitImage: opts.Image,
	})
	if err != nil {
		// ResolveAndValidateImage already prints appropriate errors
		return err
	}
	if resolvedImage == nil {
		cmdutil.PrintError("No image specified and no default image configured")
		cmdutil.PrintNextSteps(
			"Specify an image: clawker container create --agent myagent IMAGE",
			"Set default_image in clawker.yaml",
			"Set default_image in ~/.local/clawker/settings.yaml",
			"Build a project image: clawker build",
		)
		return fmt.Errorf("no image specified")
	}
	opts.Image = resolvedImage.Reference

	// Resolve container name
	agent := opts.Agent
	if agent == "" && opts.Name == "" {
		agent = docker.GenerateRandomName()
	}

	containerName := opts.Name
	if containerName == "" {
		containerName = docker.ContainerName(cfg.Project, agent)
	}

	// Setup workspace mounts
	workspaceMounts, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: opts.Mode,
		Config:       cfg,
		AgentName:    agent,
	})
	if err != nil {
		return err
	}

	// Start host proxy server for container-to-host communication (if enabled)
	hostProxyRunning := false
	if cfg.Security.HostProxyEnabled() {
		if err := f.EnsureHostProxy(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cmdutil.PrintWarning("Host proxy failed to start. Browser authentication may not work.")
			cmdutil.PrintNextSteps("To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
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
		cmdutil.PrintError("Invalid configuration: %v", err)
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
		cmdutil.HandleError(err)
		return err
	}

	// Print warnings if any
	for _, warning := range resp.Warnings {
		fmt.Fprintln(os.Stderr, "Warning:", warning)
	}

	// Output container ID (short 12-char) to stdout
	fmt.Println(resp.ID[:12])
	return nil
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
		cfg.Cmd = strslice.StrSlice(opts.Command)
	}

	// Set entrypoint if provided
	if opts.Entrypoint != "" {
		cfg.Entrypoint = strslice.StrSlice{opts.Entrypoint}
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
