package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/mount"
	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/credentials"
	"github.com/schmitthub/clawker/internal/engine"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/workspace"
	pkgbuild "github.com/schmitthub/clawker/pkg/build"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// RunOptions contains the options for the run command.
type RunOptions struct {
	Mode      string
	Build     bool
	Shell     bool   // Run shell instead of claude
	ShellPath string // Path to shell executable (overrides config)
	ShellUser string // User to run shell as
	Remove    bool   // Remove container after exit (ephemeral mode)
	Detach    bool   // Run in background (detached mode)
	Clean     bool   // Remove existing container/volumes before starting
	Agent     string // Agent name for the container
	Args      []string
	Ports     []string
}

// NewCmdRun creates the run command.
func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	opts := &RunOptions{}

	cmd := &cobra.Command{
		Use:     "run [flags] [-- <command>...]",
		Aliases: []string{"start"},
		Short:   "Build and run Claude in a container",
		Long: `Builds the container image (if needed), creates volumes, and runs Claude.

This is an idempotent operation:
  - If a container is already running, attaches to it
  - If a container exists but is stopped, starts and attaches
  - If no container exists, creates and starts one

Use --remove for ephemeral containers that are deleted on exit.

Workspace modes:
  --mode=bind      Live sync with host filesystem (default)
  --mode=snapshot  Copy files to ephemeral Docker volume`,
		Example: `  # Run Claude interactively (container preserved after exit)
  clawker run

  # Run Claude with a prompt
  clawker run -- -p "build a feature"

  # Resume previous session
  clawker run -- --resume

  # Run in snapshot mode
  clawker run --mode=snapshot

  # Run in background
  clawker run --detach

  # Run ephemeral container (removed on exit)
  clawker run --remove

  # Open a shell session
  clawker run --shell

  # Open shell with specific shell and user
  clawker run --shell -s /bin/zsh -u root

  # Publish ports to access services
  clawker run -p 8080:8080`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			return runRun(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Mode, "mode", "m", "", "Workspace mode: bind or snapshot (default from config)")
	cmd.Flags().BoolVar(&opts.Build, "build", false, "Force rebuild of the container image")
	cmd.Flags().BoolVar(&opts.Shell, "shell", false, "Run shell instead of claude")
	cmd.Flags().StringVarP(&opts.ShellPath, "shell-path", "s", "", "Path to shell executable (default from config or /bin/bash)")
	cmd.Flags().StringVarP(&opts.ShellUser, "user", "u", "", "User to run shell as (only with --shell)")
	cmd.Flags().BoolVarP(&opts.Remove, "remove", "r", false, "Remove container and volumes on exit (ephemeral mode)")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Run container in background (detached mode)")
	cmd.Flags().BoolVar(&opts.Clean, "clean", false, "Remove existing container and volumes before starting")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name for the container (default: random)")
	cmd.Flags().StringArrayVarP(&opts.Ports, "publish", "p", nil, "Publish container port(s) to host (e.g., -p 8080:8080)")

	return cmd
}

func runRun(f *cmdutil.Factory, opts *RunOptions) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			cmdutil.PrintError("No clawker.yaml found in current directory")
			cmdutil.PrintNextSteps(
				"Run 'clawker init' to create a configuration",
				"Or change to a directory with clawker.yaml",
			)
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	validator := config.NewValidator(f.WorkDir)
	if err := validator.Validate(cfg); err != nil {
		cmdutil.PrintError("Configuration validation failed")
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("mode", opts.Mode).
		Bool("shell", opts.Shell).
		Bool("remove", opts.Remove).
		Bool("detach", opts.Detach).
		Bool("clean", opts.Clean).
		Msg("starting container")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	// Determine workspace mode
	mode, err := determineMode(cfg, opts.Mode)
	if err != nil {
		return err
	}

	// Generate agent name if not provided
	agentName := opts.Agent
	if agentName == "" {
		agentName = engine.GenerateRandomName()
	}

	// Clean if requested (now that we know the agent name)
	if opts.Clean {
		if err := cleanupResources(ctx, eng, cfg.Project, agentName); err != nil {
			logger.Warn().Err(err).Msg("cleanup encountered errors")
		}
	}

	logger.Info().
		Str("project", cfg.Project).
		Str("agent", agentName).
		Str("mode", string(mode)).
		Bool("ephemeral", opts.Remove).
		Msg("starting Claude container")

	// Build or ensure image
	imageTag := engine.ImageTag(cfg.Project)
	builder := build.NewBuilder(eng, cfg, f.WorkDir)
	buildOpts := build.Options{
		ForceBuild: opts.Build,
		NoCache:    false, // NoCache only available via 'clawker build'
	}
	if err := builder.EnsureImage(ctx, imageTag, buildOpts); err != nil {
		return err
	}

	// Setup workspace strategy
	wsStrategy, err := setupWorkspace(ctx, eng, cfg, mode, f.WorkDir, agentName)
	if err != nil {
		return err
	}

	// Ensure clawker network exists
	if err := eng.EnsureNetwork(config.ClawkerNetwork); err != nil {
		logger.Warn().Err(err).Msg("failed to ensure clawker network")
		// Don't fail hard, container can still run without the network
	}

	// Check if monitoring stack is active
	monitoringActive := eng.IsMonitoringActive()
	if monitoringActive {
		logger.Info().Msg("monitoring stack detected, enabling telemetry")
	}

	// Build container configuration
	containerCfg, err := buildRunContainerConfig(cfg, imageTag, wsStrategy, f.WorkDir, agentName, f.Version, opts, monitoringActive)
	if err != nil {
		return err
	}

	// Create or find container (idempotent)
	containerMgr := engine.NewContainerManager(eng)
	containerID, created, err := containerMgr.FindOrCreate(containerCfg)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	if created {
		logger.Info().
			Str("container", containerCfg.Name).
			Str("mode", string(mode)).
			Bool("ephemeral", opts.Remove).
			Msg("created new container")
	} else {
		logger.Info().
			Str("container", containerCfg.Name).
			Msg("using existing container")
	}

	// Setup cleanup on exit if --remove
	if opts.Remove {
		defer func() {
			// Remove container first
			if err := containerMgr.Remove(containerID, true); err != nil {
				logger.Warn().Err(err).Msg("failed to remove ephemeral container")
			} else {
				logger.Info().Str("container_id", containerID[:12]).Msg("removed ephemeral container")
			}

			// Remove associated volumes by name (they may not have labels)
			volumes := []string{
				engine.VolumeName(cfg.Project, agentName, "workspace"),
				engine.VolumeName(cfg.Project, agentName, "config"),
				engine.VolumeName(cfg.Project, agentName, "history"),
			}
			for _, vol := range volumes {
				if err := eng.VolumeRemove(vol, true); err != nil {
					// Ignore errors - volume may not exist (e.g., bind mode has no workspace volume)
					logger.Debug().Str("volume", vol).Err(err).Msg("failed to remove volume")
				} else {
					logger.Debug().Str("volume", vol).Msg("removed volume")
				}
			}
		}()
	}

	// Handle detached mode
	if opts.Detach {
		// Start container if not already running
		if err := containerMgr.Start(containerID); err != nil {
			// Ignore "already started" errors
			logger.Debug().Err(err).Msg("start returned error (may be already running)")
		}
		fmt.Fprintf(os.Stderr, "Container %s is running in detached mode\n", containerCfg.Name)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  clawker logs      # View container logs")
		fmt.Fprintln(os.Stderr, "  clawker run --shell  # Open shell in container")
		fmt.Fprintln(os.Stderr, "  clawker stop      # Stop the container")
		return nil
	}

	// Attach to container, start it, then stream I/O
	return attachAndRun(ctx, containerMgr, containerID)
}

func determineMode(cfg *config.Config, modeFlag string) (config.Mode, error) {
	if modeFlag != "" {
		return config.ParseMode(modeFlag)
	}
	return config.ParseMode(cfg.Workspace.DefaultMode)
}

func setupWorkspace(ctx context.Context, eng *engine.Engine, cfg *config.Config, mode config.Mode, workDir string, agentName string) (workspace.Strategy, error) {
	// Load ignore patterns
	ignorePatterns, err := engine.LoadIgnorePatterns(filepath.Join(workDir, config.IgnoreFileName))
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load ignore patterns")
	}

	// Create workspace strategy
	wsConfig := workspace.Config{
		HostPath:       workDir,
		RemotePath:     cfg.Workspace.RemotePath,
		ProjectName:    cfg.Project,
		AgentName:      agentName,
		IgnorePatterns: ignorePatterns,
	}

	strategy, err := workspace.NewStrategy(mode, wsConfig)
	if err != nil {
		return nil, err
	}

	// Prepare workspace (creates volumes for snapshot mode)
	if err := strategy.Prepare(ctx, eng); err != nil {
		return nil, fmt.Errorf("failed to prepare workspace: %w", err)
	}

	// Ensure config and history volumes exist with proper labels
	if err := workspace.EnsureConfigVolumes(eng, cfg.Project, agentName); err != nil {
		return nil, fmt.Errorf("failed to create config volumes: %w", err)
	}

	return strategy, nil
}

// resolveShellPath resolves the shell path using Viper configuration hierarchy:
// 1. CLI flag (opts.ShellPath) - highest priority
// 2. CLAWKER_SHELL environment variable
// 3. agent.shell from clawker.yaml
// 4. Default: /bin/bash
func resolveShellPath(opts *RunOptions) string {
	// CLI flag takes highest precedence
	if opts.ShellPath != "" {
		return opts.ShellPath
	}

	// Check Viper (env var and config file)
	if shellPath := viper.GetString("agent.shell"); shellPath != "" {
		return shellPath
	}

	// Default
	return "/bin/bash"
}

func buildRunContainerConfig(cfg *config.Config, imageTag string, wsStrategy workspace.Strategy, workDir string, agentName string, version string, opts *RunOptions, monitoringActive bool) (engine.ContainerConfig, error) {
	// Build environment variables
	envBuilder := credentials.NewEnvBuilder()

	// Add config-specified environment
	envBuilder.SetAll(cfg.Agent.Env)

	// Inject clawker identity for statusline
	envBuilder.Set("CLAWKER_PROJECT", cfg.Project)
	envBuilder.Set("CLAWKER_AGENT", agentName)

	// Load .env file if present
	envFile := filepath.Join(workDir, ".env")
	if err := envBuilder.LoadDotEnv(envFile); err != nil {
		logger.Warn().Err(err).Msg("failed to load .env file")
	}

	// Add useful passthrough variables
	envBuilder.SetFromHostAll(credentials.DefaultPassthrough())

	// Add OTEL environment variables if monitoring is active
	containerName := engine.ContainerName(cfg.Project, agentName)
	if monitoringActive {
		envBuilder.SetAll(credentials.OtelEnvVars(containerName))
	}

	// Build mounts
	var mounts []mount.Mount

	// Add workspace mount
	mounts = append(mounts, wsStrategy.GetMounts()...)

	// Add config volume mounts (persistent across sessions)
	mounts = append(mounts, workspace.GetConfigVolumeMounts(cfg.Project, agentName)...)

	// Add Docker socket if enabled
	if cfg.Security.DockerSocket {
		mounts = append(mounts, workspace.GetDockerSocketMount())
	}

	// Build capabilities
	var capAdd []string
	if cfg.Security.EnableFirewall {
		capAdd = append(capAdd, "NET_ADMIN", "NET_RAW")
	}
	capAdd = append(capAdd, cfg.Security.CapAdd...)

	// Determine command to run
	var cmd []string
	if opts.Shell {
		shellPath := resolveShellPath(opts)
		cmd = []string{shellPath}
	} else if len(opts.Args) > 0 {
		cmd = opts.Args
	}
	// If no args and not shell, cmd is empty and entrypoint handles default (claude)

	// Determine user
	user := fmt.Sprintf("%d:%d", pkgbuild.DefaultUID, pkgbuild.DefaultGID)
	if opts.Shell && opts.ShellUser != "" {
		user = opts.ShellUser
	}

	// Parse port bindings
	portBindings, exposedPorts, err := engine.ParsePortSpecs(opts.Ports)
	if err != nil {
		return engine.ContainerConfig{}, fmt.Errorf("invalid port specification: %w", err)
	}

	return engine.ContainerConfig{
		Name:         containerName,
		Image:        imageTag,
		Mounts:       mounts,
		Env:          envBuilder.Build(),
		WorkingDir:   cfg.Workspace.RemotePath,
		Cmd:          cmd,
		Tty:          true,
		OpenStdin:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		CapAdd:       capAdd,
		User:         user,
		NetworkMode:  config.ClawkerNetwork,
		Labels:       engine.ContainerLabels(cfg.Project, agentName, version, imageTag, workDir),
		PortBindings: portBindings,
		ExposedPorts: exposedPorts,
	}, nil
}

// attachAndRun attaches to a container, starts it, and streams I/O.
// The order (attach before start) is critical to capture output from fast commands.
func attachAndRun(ctx context.Context, containerMgr *engine.ContainerManager, containerID string) error {
	// Setup PTY handler
	pty := term.NewPTYHandler()

	// Setup terminal
	if err := pty.Setup(); err != nil {
		logger.Warn().Err(err).Msg("failed to setup terminal, continuing without raw mode")
	}
	defer pty.Restore()

	// Attach to container BEFORE starting (critical for capturing output)
	hijacked, err := containerMgr.Attach(containerID)
	if err != nil {
		return err
	}

	// Now start the container
	if err := containerMgr.Start(containerID); err != nil {
		// Ignore "already started" errors - container may be reused
		logger.Debug().Err(err).Msg("start returned error (may be already running)")
	}
	logger.Debug().Str("container_id", containerID[:12]).Msg("container started")

	// Setup resize handler
	if pty.IsTerminal() {
		resizeHandler := term.NewResizeHandler(
			func(height, width uint) error {
				return containerMgr.Resize(containerID, height, width)
			},
			pty.GetSize,
		)
		resizeHandler.Start()
		defer resizeHandler.Stop()

		// Initial resize
		resizeHandler.TriggerResize()
	}

	// Stream I/O
	fmt.Println() // Clear line before attaching
	if err := pty.StreamWithResize(ctx, hijacked, func(height, width uint) error {
		return containerMgr.Resize(containerID, height, width)
	}); err != nil {
		if err == context.Canceled {
			return nil
		}
		return err
	}

	// Wait for container to exit
	exitCode, err := containerMgr.Wait(containerID)
	if err != nil {
		return err
	}

	if exitCode != 0 {
		logger.Debug().Int64("exit_code", exitCode).Msg("container exited with non-zero status")
		// Must restore terminal before os.Exit since defers don't run
		pty.Restore()
		os.Exit(int(exitCode))
	}

	return nil
}

func cleanupResources(_ context.Context, eng *engine.Engine, projectName, agentName string) error {
	containerMgr := engine.NewContainerManager(eng)
	volumeMgr := engine.NewVolumeManager(eng)

	// Remove container
	containerName := engine.ContainerName(projectName, agentName)
	existing, err := eng.FindContainerByName(containerName)
	if err != nil {
		return err
	}
	if existing != nil {
		logger.Info().Str("container", containerName).Msg("removing existing container")
		if err := containerMgr.Remove(existing.ID, true); err != nil {
			logger.Warn().Err(err).Msg("failed to remove container")
		}
	}

	// Remove volumes
	volumes := []string{
		engine.VolumeName(projectName, agentName, "workspace"),
		engine.VolumeName(projectName, agentName, "config"),
		engine.VolumeName(projectName, agentName, "history"),
	}

	if err := volumeMgr.RemoveVolumes(volumes, true); err != nil {
		logger.Warn().Err(err).Msg("failed to remove some volumes")
	}

	return nil
}
