package run

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

// RunOptions contains the options for the run command.
type RunOptions struct {
	Mode  string
	Build bool
	Shell bool     // Run shell instead of claude
	Keep  bool     // Keep container after exit (inverse of --rm default)
	Agent string   // Agent name for the container
	Args  []string // Command/args to run in container (after --)
}

// NewCmdRun creates the run command.
func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	opts := &RunOptions{}

	cmd := &cobra.Command{
		Use:   "run [flags] [-- <command>...]",
		Short: "Run a one-shot command in an ephemeral container",
		Long: `Runs a command in a new container and removes it when done.

By default, the container is removed after exit (like docker run --rm).
Use --keep to preserve the container after exit.

Unlike 'claucker start', this always creates a new container (never reuses existing).`,
		Example: `  # Run claude interactively, remove on exit
  claucker run

  # Run claude with args, remove on exit
  claucker run -- -p "build a feature"

  # Run shell interactively, remove on exit
  claucker run --shell

  # Run arbitrary command, remove on exit
  claucker run -- npm test

  # Run claude, keep container after exit
  claucker run --keep`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			return runRun(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Mode, "mode", "m", "", "Workspace mode: bind or snapshot (default from config)")
	cmd.Flags().BoolVar(&opts.Build, "build", false, "Force rebuild of the container image")
	cmd.Flags().BoolVar(&opts.Shell, "shell", false, "Run shell instead of claude")
	cmd.Flags().BoolVar(&opts.Keep, "keep", false, "Keep container after exit (default: remove)")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name for the container (default: random)")

	return cmd
}

func runRun(f *cmdutil.Factory, opts *RunOptions) error {
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
		Bool("shell", opts.Shell).
		Bool("keep", opts.Keep).
		Msg("starting ephemeral run")

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

	logger.Info().
		Str("project", cfg.Project).
		Str("agent", agentName).
		Str("mode", string(mode)).
		Bool("ephemeral", !opts.Keep).
		Msg("starting ephemeral container")

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
	containerCfg, err := buildRunContainerConfig(cfg, imageTag, wsStrategy, f.WorkDir, agentName, f.Version, opts, monitoringActive)
	if err != nil {
		return err
	}

	// Create container (but don't start yet - we need to attach first to capture output)
	containerMgr := engine.NewContainerManager(eng)
	containerID, err := containerMgr.Create(containerCfg)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	logger.Info().
		Str("container_id", containerID[:12]).
		Bool("ephemeral", !opts.Keep).
		Msg("created ephemeral container")

	// Setup cleanup on exit unless --keep
	if !opts.Keep {
		defer func() {
			if err := containerMgr.Remove(containerID, true); err != nil {
				logger.Warn().Err(err).Msg("failed to remove ephemeral container")
			} else {
				logger.Info().Str("container_id", containerID[:12]).Msg("removed ephemeral container")
			}
		}()
	}

	// Attach to container, start it, then stream I/O
	// This order is critical: attach before start to capture output from fast commands
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

	return strategy, nil
}

func buildRunContainerConfig(cfg *config.Config, imageTag string, wsStrategy workspace.Strategy, workDir string, agentName string, version string, opts *RunOptions, monitoringActive bool) (engine.ContainerConfig, error) {
	// Build environment variables
	envBuilder := credentials.NewEnvBuilder()

	// Add config-specified environment
	envBuilder.SetAll(cfg.Agent.Env)

	// Inject claucker identity for statusline
	envBuilder.Set("CLAUCKER_PROJECT", cfg.Project)
	envBuilder.Set("CLAUCKER_AGENT", agentName)

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
		cmd = []string{"/bin/bash"}
	} else if len(opts.Args) > 0 {
		cmd = opts.Args
	}
	// If no args and not shell, cmd is empty and entrypoint handles default (claude)

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
		User:         fmt.Sprintf("%d:%d", pkgbuild.DefaultUID, pkgbuild.DefaultGID),
		NetworkMode:  config.ClauckerNetwork,
		Labels:       engine.ContainerLabels(cfg.Project, agentName, version, imageTag, workDir),
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
		return fmt.Errorf("failed to start container: %w", err)
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
