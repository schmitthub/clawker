package start

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/mount"
	"github.com/schmitthub/claucker/internal/build"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/credentials"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/internal/workspace"
	pkgbuild "github.com/schmitthub/claucker/pkg/build"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// StartOptions contains the options for the start command.
type StartOptions struct {
	Mode   string
	Build  bool
	Detach bool
	Clean  bool
	Agent  string   // Agent name for the container
	Args   []string // Arguments to pass to claude CLI (after --)
}

// NewCmdStart creates the start command.
func NewCmdStart(f *cmdutil.Factory) *cobra.Command {
	opts := &StartOptions{}

	cmd := &cobra.Command{
		Use:   "start [-- <claude-args>...]",
		Short: "Build and run Claude in a container",
		Long: `Builds the container image (if needed), creates volumes, and runs Claude.

This is an idempotent operation:
  - If a container is already running, attaches to it
  - If a container exists but is stopped, starts and attaches
  - If no container exists, creates and starts one

Workspace modes:
  --mode=bind      Live sync with host filesystem (default)
  --mode=snapshot  Copy files to ephemeral Docker volume`,
		Example: `  # Start Claude interactively
  claucker start

  # Start with a prompt
  claucker start -- -p "build a feature"

  # Resume previous session
  claucker start -- --resume

  # Start in snapshot mode
  claucker start --mode=snapshot

  # Start in background
  claucker start --detach`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			return runStart(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Mode, "mode", "m", "", "Workspace mode: bind or snapshot (default from config)")
	cmd.Flags().BoolVar(&opts.Build, "build", false, "Force rebuild of the container image")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Run container in background (detached mode)")
	cmd.Flags().BoolVar(&opts.Clean, "clean", false, "Remove existing container and volumes before starting")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name for the container (default: random)")

	return cmd
}

func runStart(f *cmdutil.Factory, opts *StartOptions) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			cmdutil.PrintError("No claucker.yaml found in current directory")
			cmdutil.PrintNextSteps(
				"Run 'claucker init' to create a configuration",
				"Or change to a directory with claucker.yaml",
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
		Bool("build", opts.Build).
		Bool("detach", opts.Detach).
		Msg("starting up")

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
		Msg("starting Claude container")

	// Build or ensure image
	imageTag := engine.ImageTag(cfg.Project)
	builder := build.NewBuilder(eng, cfg, f.WorkDir)
	buildOpts := build.Options{
		ForceBuild: opts.Build,
		NoCache:    false, // NoCache only available via 'claucker build'
	}
	if err := builder.EnsureImage(ctx, imageTag, buildOpts); err != nil {
		return err
	}

	// Setup workspace strategy
	wsStrategy, err := setupWorkspace(ctx, eng, cfg, mode, f.WorkDir, agentName)
	if err != nil {
		return err
	}

	// Ensure claucker network exists
	if err := eng.EnsureNetwork(config.ClauckerNetwork); err != nil {
		logger.Warn().Err(err).Msg("failed to ensure claucker network")
		// Don't fail hard, container can still run without the network
	}

	// Check if monitoring stack is active
	monitoringActive := eng.IsMonitoringActive()
	if monitoringActive {
		logger.Info().Msg("monitoring stack detected, enabling telemetry")
	}

	// Build container configuration
	containerCfg, err := buildContainerConfig(cfg, imageTag, wsStrategy, f.WorkDir, agentName, f.Version, opts.Args, monitoringActive)
	if err != nil {
		return err
	}

	// Create or find container
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
			Msg("created new container")
	} else {
		logger.Info().
			Str("container", containerCfg.Name).
			Msg("using existing container")
	}

	// Handle detached mode
	if opts.Detach {
		fmt.Fprintf(os.Stderr, "Container %s is running in detached mode\n", containerCfg.Name)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  claucker logs      # View container logs")
		fmt.Fprintln(os.Stderr, "  claucker sh        # Open shell in container")
		fmt.Fprintln(os.Stderr, "  claucker stop      # Stop the container")
		return nil
	}

	// Attach to container
	return attachToContainer(ctx, eng, containerMgr, containerID)
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

	return strategy, nil
}

func buildContainerConfig(cfg *config.Config, imageTag string, wsStrategy workspace.Strategy, workDir string, agentName string, version string, claudeArgs []string, monitoringActive bool) (engine.ContainerConfig, error) {
	// Build environment variables
	envBuilder := credentials.NewEnvBuilder()

	// Add config-specified environment
	envBuilder.SetAll(cfg.Agent.Env)

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

	return engine.ContainerConfig{
		Name:         containerName,
		Image:        imageTag,
		Mounts:       mounts,
		Env:          envBuilder.Build(),
		WorkingDir:   cfg.Workspace.RemotePath,
		Cmd:          claudeArgs,
		Tty:          true,
		OpenStdin:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		CapAdd:       capAdd,
		User:         fmt.Sprintf("%d:%d", pkgbuild.DefaultUID, pkgbuild.DefaultGID),
		NetworkMode:  config.ClauckerNetwork,
		Labels:       engine.ContainerLabels(cfg.Project, agentName, version, imageTag, workDir),
	}, nil
}

func attachToContainer(ctx context.Context, eng *engine.Engine, containerMgr *engine.ContainerManager, containerID string) error {
	// Setup PTY handler
	pty := term.NewPTYHandler()

	// Setup terminal
	if err := pty.Setup(); err != nil {
		logger.Warn().Err(err).Msg("failed to setup terminal, continuing without raw mode")
	}
	defer pty.Restore()

	// Attach to container
	hijacked, err := containerMgr.Attach(containerID)
	if err != nil {
		return err
	}

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

func cleanupResources(ctx context.Context, eng *engine.Engine, projectName, agentName string) error {
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
