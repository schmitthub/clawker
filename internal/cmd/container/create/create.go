// Package create provides the container create command.
package create

import (
	"context"
	"fmt"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
)

// Options holds options for the create command.
// It embeds ContainerOptions for shared container configuration.
type Options struct {
	*copts.ContainerOptions
}

// NewCmd creates a new container create command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &Options{ContainerOptions: containerOpts}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create a new container",
		Long: `Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml. When --name is provided, it overrides this.

If IMAGE is "@", clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag`,
		Example: `  # Create a container with a specific agent name
  clawker container create --agent myagent alpine

  # Create a container using default image from config
  clawker container create --agent myagent @

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
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerOpts.Image = args[0]
			if len(args) > 1 {
				containerOpts.Command = args[1:]
			}
			return run(cmd.Context(), f, opts)
		},
	}

	// Add shared container flags
	copts.AddFlags(cmd.Flags(), containerOpts)
	copts.MarkMutuallyExclusive(cmd)

	// Stop parsing flags after the first positional argument (IMAGE).
	// This allows flags after IMAGE to be passed to the container command.
	// Example: clawker create alpine --version
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

	// adding a defensive check here in case both --name and --agent end up being set due to regression
	if containerOpts.Name != "" && containerOpts.Agent != "" && containerOpts.Name != containerOpts.Agent {
		cmdutil.PrintError(ios, "Cannot use both --name and --agent")
		return fmt.Errorf("conflicting container naming options")
	}

	agentName := containerOpts.GetAgentName()
	if agentName == "" {
		agentName = docker.GenerateRandomName()
	}

	// Setup workspace mounts
	workspaceMounts, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: containerOpts.Mode,
		Config:       cfg,
		AgentName:    agentName,
	})
	if err != nil {
		return err
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
	containerConfig, hostConfig, networkConfig, err := containerOpts.BuildConfigs(workspaceMounts, cfg)
	if err != nil {
		cmdutil.PrintError(ios, "Invalid configuration: %v", err)
		return err
	}

	// Build extra labels for clawker metadata
	extraLabels := map[string]string{
		docker.LabelProject: cfg.Project,
	}
	extraLabels[docker.LabelAgent] = agentName

	containerName := docker.ContainerName(cfg.Project, agentName)

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

	// Print warnings if any
	for _, warning := range resp.Warnings {
		fmt.Fprintln(ios.ErrOut, "Warning:", warning)
	}

	// Output container ID (short 12-char) to stdout
	fmt.Fprintln(ios.Out, resp.ID[:12])
	return nil
}
