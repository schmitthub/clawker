// Package create provides the container create command.
package create

import (
	"context"
	"fmt"

	intbuild "github.com/schmitthub/clawker/internal/build"
	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"

	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CreateOptions holds options for the create command.
// It embeds ContainerOptions for shared container configuration.
type CreateOptions struct {
	*copts.ContainerOptions

	IOStreams  *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config
	HostProxy func() *hostproxy.Manager
	Prompter  func() *prompter.Prompter
	WorkDir   func() string

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmdCreate creates a new container create command.
func NewCmdCreate(f *cmdutil.Factory, runF func(context.Context, *CreateOptions) error) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &CreateOptions{
		ContainerOptions: containerOpts,
		IOStreams:         f.IOStreams,
		Client:           f.Client,
		Config:           f.Config,
		HostProxy:        f.HostProxy,
		Prompter:         f.Prompter,
		WorkDir:          f.WorkDir,
	}

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
			return createRun(cmd.Context(), opts)
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

func createRun(ctx context.Context, opts *CreateOptions) error {
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

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Resolve image name
	if containerOpts.Image == "@" {
		resolvedImage, err := client.ResolveImageWithSource(ctx)
		if err != nil {
			return err
		}
		if resolvedImage == nil {
			cmdutil.PrintError(ios, "No image specified and no default image configured")
			cmdutil.PrintNextSteps(ios,
				"Specify an image: clawker container create IMAGE",
				"Set default_image in clawker.yaml",
				"Set default_image in ~/.local/clawker/settings.yaml",
				"Build a project image: clawker build",
			)
			return fmt.Errorf("no image specified")
		}

		// For default images, verify the image exists and offer to rebuild if missing
		if resolvedImage.Source == docker.ImageSourceDefault {
			exists, err := client.ImageExists(ctx, resolvedImage.Reference)
			if err != nil {
				logger.Debug().Err(err).Str("image", resolvedImage.Reference).Msg("failed to check if image exists")
			} else if !exists {
				if err := handleMissingDefaultImage(ctx, opts, cfgGateway, resolvedImage.Reference); err != nil {
					return err
				}
			}
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
		WorkDir:      opts.WorkDir(),
	})
	if err != nil {
		return err
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
			hostProxyRunning = true
			// Inject host proxy URL into container environment
			if hp.IsRunning() {
				envVar := "CLAWKER_HOST_PROXY=" + hp.ProxyURL()
				containerOpts.Env = append(containerOpts.Env, envVar)
			}
		}
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

// handleMissingDefaultImage prompts the user to rebuild a missing default image.
// In non-interactive mode, it prints instructions and returns an error.
func handleMissingDefaultImage(ctx context.Context, opts *CreateOptions, cfgGateway *config.Config, imageRef string) error {
	ios := opts.IOStreams

	if !ios.IsInteractive() {
		cmdutil.PrintError(ios, "Default image %q not found", imageRef)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker create IMAGE",
			"Or build a project image: clawker build",
		)
		return fmt.Errorf("default image %q not found", imageRef)
	}

	// Interactive mode — prompt to rebuild
	p := opts.Prompter()
	options := []prompter.SelectOption{
		{Label: "Yes", Description: "Rebuild the default base image now"},
		{Label: "No", Description: "Cancel and fix manually"},
	}

	idx, err := p.Select(
		fmt.Sprintf("Default image %q not found. Rebuild now?", imageRef),
		options,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to prompt for rebuild: %w", err)
	}

	if idx != 0 {
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker create IMAGE",
			"Or build a project image: clawker build",
		)
		return fmt.Errorf("default image %q not found", imageRef)
	}

	// Get flavor selection
	flavors := intbuild.DefaultFlavorOptions()
	flavorOptions := make([]prompter.SelectOption, len(flavors))
	for i, f := range flavors {
		flavorOptions[i] = prompter.SelectOption{
			Label:       f.Name,
			Description: f.Description,
		}
	}

	flavorIdx, err := p.Select("Select Linux flavor", flavorOptions, 0)
	if err != nil {
		return fmt.Errorf("failed to select flavor: %w", err)
	}

	selectedFlavor := flavors[flavorIdx].Name
	fmt.Fprintf(ios.ErrOut, "Building %s...\n", intbuild.DefaultImageTag)

	if err := intbuild.BuildDefaultImage(ctx, selectedFlavor); err != nil {
		fmt.Fprintf(ios.ErrOut, "Error: Failed to build image: %v\n", err)
		return fmt.Errorf("failed to rebuild default image: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "Build complete! Using image: %s\n", intbuild.DefaultImageTag)

	// Persist the default image in settings
	settingsLoader, err := cfgGateway.SettingsLoader()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load settings loader; default image will not be persisted")
	} else if settingsLoader != nil {
		currentSettings, loadErr := settingsLoader.Load()
		if loadErr != nil {
			logger.Warn().Err(loadErr).Msg("failed to load existing settings; skipping settings update")
		} else {
			currentSettings.DefaultImage = intbuild.DefaultImageTag
			if saveErr := settingsLoader.Save(currentSettings); saveErr != nil {
				logger.Warn().Err(saveErr).Msg("failed to update settings with default image")
			}
		}
	}

	return nil
}
