// Package create provides the container create command.
package create

import (
	"context"
	"fmt"
	"os"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CreateOptions holds options for the create command.
// It embeds ContainerOptions for shared container configuration.
type CreateOptions struct {
	*copts.ContainerOptions

	IOStreams   *iostreams.IOStreams
	TUI        *tui.TUI
	Client     func(context.Context) (*docker.Client, error)
	Config     func() *config.Config
	GitManager func() (*git.GitManager, error)
	HostProxy  func() *hostproxy.Manager
	Prompter   func() *prompter.Prompter

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmdCreate creates a new container create command.
func NewCmdCreate(f *cmdutil.Factory, runF func(context.Context, *CreateOptions) error) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &CreateOptions{
		ContainerOptions: containerOpts,
		IOStreams:   f.IOStreams,
		TUI:        f.TUI,
		Client:     f.Client,
		Config:     f.Config,
		GitManager: f.GitManager,
		HostProxy:  f.HostProxy,
		Prompter:   f.Prompter,
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

	// Get project config
	cfg := cfgGateway.Project

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Resolve image name
	if containerOpts.Image == "@" {
		resolvedImage, err := client.ResolveImageWithSource(ctx)
		if err != nil {
			return err
		}
		if resolvedImage == nil {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s No image specified and no default image configured\n", cs.FailureIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. Specify an image: clawker container create IMAGE")
			fmt.Fprintln(ios.ErrOut, "  2. Set default_image in clawker.yaml")
			fmt.Fprintln(ios.ErrOut, "  3. Set default_image in ~/.local/clawker/settings.yaml")
			fmt.Fprintln(ios.ErrOut, "  4. Build a project image: clawker build")
			return cmdutil.SilentError
		}

		// For default images, verify the image exists and offer to rebuild if missing
		if resolvedImage.Source == docker.ImageSourceDefault {
			exists, err := client.ImageExists(ctx, resolvedImage.Reference)
			if err != nil {
				logger.Warn().Err(err).Str("image", resolvedImage.Reference).Msg("failed to check if image exists")
			} else if !exists {
				if err := shared.RebuildMissingDefaultImage(ctx, shared.RebuildMissingImageOpts{
					ImageRef:       resolvedImage.Reference,
					IOStreams:      ios,
					TUI:           opts.TUI,
					Prompter:       opts.Prompter,
					SettingsLoader: func() config.SettingsLoader { return cfgGateway.SettingsLoader() },
					BuildImage:     client.BuildDefaultImage,
					CommandVerb:    "create",
				}); err != nil {
					return err
				}
			}
		}

		containerOpts.Image = resolvedImage.Reference
	}

	// adding a defensive check here in case both --name and --agent end up being set due to regression
	if containerOpts.Name != "" && containerOpts.Agent != "" && containerOpts.Name != containerOpts.Agent {
		return fmt.Errorf("cannot use both --name and --agent")
	}

	agentName := containerOpts.GetAgentName()
	if agentName == "" {
		agentName = docker.GenerateRandomName()
	}

	// Determine working directory based on --worktree flag
	var wd string
	var projectRootDir string // Set when using worktree, for .git mount
	if containerOpts.Worktree != "" {
		// Use git worktree as workspace source
		wtSpec, err := cmdutil.ParseWorktreeFlag(containerOpts.Worktree, agentName)
		if err != nil {
			return fmt.Errorf("invalid --worktree flag: %w", err)
		}

		gitMgr, err := opts.GitManager()
		if err != nil {
			return fmt.Errorf("cannot use --worktree: %w", err)
		}

		wd, err = gitMgr.SetupWorktree(cfg, wtSpec.Branch, wtSpec.Base)
		if err != nil {
			return fmt.Errorf("setting up worktree %q for agent %q: %w", wtSpec.Branch, agentName, err)
		}
		logger.Debug().Str("worktree", wd).Str("branch", wtSpec.Branch).Msg("using git worktree")

		// Capture project root for mounting .git in container.
		// Worktrees use a .git file that references the main repo's .git directory.
		projectRootDir = cfg.RootDir()
	} else {
		// Get working directory from project root, or fall back to current directory
		wd = cfg.RootDir()
		if wd == "" {
			var wdErr error
			wd, wdErr = os.Getwd()
			if wdErr != nil {
				return fmt.Errorf("failed to get working directory: %w", wdErr)
			}
		}
	}

	// Setup workspace mounts
	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride:   containerOpts.Mode,
		Config:         cfg,
		AgentName:      agentName,
		WorkDir:        wd,
		ProjectRootDir: projectRootDir,
	})
	if err != nil {
		return err
	}
	workspaceMounts := wsResult.Mounts

	// Initialize container config if the config volume was freshly created.
	// This copies host Claude config and/or credentials into the volume before
	// the container starts, so the agent inherits host settings on first run.
	if wsResult.ConfigVolumeResult.ConfigCreated {
		if err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
			ProjectName:      cfg.Project,
			AgentName:        agentName,
			ContainerWorkDir: cfg.Workspace.RemotePath,
			ClaudeCode:       cfg.Agent.ClaudeCode,
			CopyToVolume:     client.CopyToVolume,
		}); err != nil {
			return fmt.Errorf("container init: %w", err)
		}
	}

	// Start host proxy server for container-to-host communication (if enabled)
	hostProxyRunning := false
	if cfg.Security.HostProxyEnabled() {
		hp := opts.HostProxy()
		if err := hp.EnsureRunning(); err != nil {
			logger.Warn().Err(err).Msg("failed to start host proxy server")
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s Host proxy failed to start. Browser authentication may not work.\n", cs.WarningIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. To disable: set 'security.enable_host_proxy: false' in clawker.yaml")
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

	// Resolve workspace mode (CLI flag overrides config default)
	workspaceMode := containerOpts.Mode
	if workspaceMode == "" {
		workspaceMode = cfg.Workspace.DefaultMode
	}

	// Inject config-derived runtime env vars (editor, firewall, terminal, agent env, instruction env)
	envOpts := docker.RuntimeEnvOpts{
		Project:         cfg.Project,
		Agent:           agentName,
		WorkspaceMode:   workspaceMode,
		WorkspaceSource: wd,
		Editor:          cfg.Agent.Editor,
		Visual:          cfg.Agent.Visual,
		Is256Color:      ios.Is256ColorSupported(),
		TrueColor:       ios.IsTrueColorSupported(),
		AgentEnv:        cfg.Agent.Env,
	}
	if cfg.Security.FirewallEnabled() {
		envOpts.FirewallEnabled = true
		envOpts.FirewallDomains = cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains)
		envOpts.FirewallOverride = cfg.Security.Firewall.IsOverrideMode()
		envOpts.FirewallIPRangeSources = cfg.Security.Firewall.GetIPRangeSources()
	}
	if cfg.Security.GitCredentials != nil {
		envOpts.GPGForwardingEnabled = cfg.Security.GitCredentials.GPGEnabled()
		envOpts.SSHForwardingEnabled = cfg.Security.GitCredentials.GitSSHEnabled()
	}
	if cfg.Build.Instructions != nil {
		envOpts.InstructionEnv = cfg.Build.Instructions.Env
	}
	runtimeEnv, err := docker.RuntimeEnv(envOpts)
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
		return fmt.Errorf("invalid configuration: %w", err)
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
		return fmt.Errorf("creating container: %w", err)
	}

	// Inject onboarding file if host auth is enabled.
	// Must happen after ContainerCreate and before ContainerStart.
	// The file marks Claude Code onboarding as complete so the user is not prompted.
	if cfg.Agent.ClaudeCode.UseHostAuthEnabled() {
		if err := shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{
			ContainerID:     resp.ID,
			CopyToContainer: shared.NewCopyToContainerFn(client),
		}); err != nil {
			return fmt.Errorf("inject onboarding: %w", err)
		}
	}

	// Print warnings if any
	for _, warning := range resp.Warnings {
		fmt.Fprintln(ios.ErrOut, "Warning:", warning)
	}

	// Output container ID (short 12-char) to stdout
	fmt.Fprintln(ios.Out, resp.ID[:12])
	return nil
}
