package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/mount"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/credentials"
	"github.com/schmitthub/claucker/internal/dockerfile"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/internal/workspace"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// RunOptions contains the options for the run command.
type RunOptions struct {
	Mode    string
	Build   bool
	NoCache bool
	Shell   bool     // Run shell instead of claude
	Keep    bool     // Keep container after exit (inverse of --rm default)
	Args    []string // Command/args to run in container (after --)
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

Examples:
  claucker run                           # Run claude interactively, remove on exit
  claucker run -- -p "build a feature"   # Run claude with args, remove on exit
  claucker run --shell                   # Run shell interactively, remove on exit
  claucker run -- npm test               # Run arbitrary command, remove on exit
  claucker run --keep                    # Run claude, keep container after exit

Unlike 'claucker up', this always creates a new container (never reuses existing).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			return runRun(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Mode, "mode", "m", "", "Workspace mode: bind or snapshot (default from config)")
	cmd.Flags().BoolVar(&opts.Build, "build", false, "Force rebuild of the container image")
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Build image without using Docker cache (implies --build)")
	cmd.Flags().BoolVar(&opts.Shell, "shell", false, "Run shell instead of claude")
	cmd.Flags().BoolVar(&opts.Keep, "keep", false, "Keep container after exit (default: remove)")

	return cmd
}

func runRun(f *cmdutil.Factory, opts *RunOptions) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			fmt.Println()
			fmt.Println("Next Steps:")
			fmt.Println("  1. Run 'claucker init' to create a configuration")
			fmt.Println("  2. Or change to a directory with claucker.yaml")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	validator := config.NewValidator(f.WorkDir)
	if err := validator.Validate(cfg); err != nil {
		fmt.Println("Error: Configuration validation failed")
		fmt.Println(err)
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
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		} else {
			fmt.Printf("Error: %s\n", err)
		}
		return err
	}
	defer eng.Close()

	// Determine workspace mode
	mode, err := determineMode(cfg, opts.Mode)
	if err != nil {
		return err
	}

	logger.Info().
		Str("project", cfg.Project).
		Str("mode", string(mode)).
		Bool("ephemeral", !opts.Keep).
		Msg("starting ephemeral container")

	// Build or ensure image
	imageTag := engine.ImageTag(cfg.Project)
	// --no-cache implies --build
	forceBuild := opts.Build || opts.NoCache
	if err := ensureImage(ctx, eng, cfg, imageTag, f.WorkDir, forceBuild, opts.NoCache); err != nil {
		return err
	}

	// Setup workspace strategy
	wsStrategy, err := setupWorkspace(ctx, eng, cfg, mode, f.WorkDir)
	if err != nil {
		return err
	}

	// Build container configuration
	containerCfg, err := buildRunContainerConfig(cfg, imageTag, wsStrategy, f.WorkDir, opts)
	if err != nil {
		return err
	}

	// Always create new container (never FindOrCreate)
	containerMgr := engine.NewContainerManager(eng)
	containerID, err := containerMgr.CreateAndStart(containerCfg)
	if err != nil {
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		}
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

	// Attach to container and wait for completion
	return attachToContainer(ctx, containerMgr, containerID)
}

func determineMode(cfg *config.Config, modeFlag string) (config.Mode, error) {
	if modeFlag != "" {
		return config.ParseMode(modeFlag)
	}
	return config.ParseMode(cfg.Workspace.DefaultMode)
}

func ensureImage(ctx context.Context, eng *engine.Engine, cfg *config.Config, imageTag, workDir string, forceBuild, noCache bool) error {
	imgMgr := engine.NewImageManager(eng)
	gen := dockerfile.NewGenerator(cfg, workDir)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", cfg.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		// Create build context from directory
		buildCtx, err := dockerfile.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return imgMgr.BuildImage(buildCtx, imageTag, filepath.Base(gen.GetCustomDockerfilePath()), nil, noCache)
	}

	// Check if image exists and we don't need to rebuild
	if !forceBuild {
		exists, err := eng.ImageExists(imageTag)
		if err != nil {
			return err
		}
		if exists {
			logger.Debug().Str("image", imageTag).Msg("image exists, skipping build")
			return nil
		}
	}

	// Generate and build Dockerfile
	logger.Info().Str("image", imageTag).Msg("building container image")

	buildCtx, err := gen.GenerateBuildContext()
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return imgMgr.BuildImage(buildCtx, imageTag, "Dockerfile", nil, noCache)
}

func setupWorkspace(ctx context.Context, eng *engine.Engine, cfg *config.Config, mode config.Mode, workDir string) (workspace.Strategy, error) {
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

func buildRunContainerConfig(cfg *config.Config, imageTag string, wsStrategy workspace.Strategy, workDir string, opts *RunOptions) (engine.ContainerConfig, error) {
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

	// Build mounts
	var mounts []mount.Mount

	// Add workspace mount
	mounts = append(mounts, wsStrategy.GetMounts()...)

	// Add config volume mounts (persistent across sessions)
	mounts = append(mounts, workspace.GetConfigVolumeMounts(cfg.Project)...)

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
		// No Name - let Docker generate unique name for ephemeral container
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
		User:         fmt.Sprintf("%d:%d", dockerfile.DefaultUID, dockerfile.DefaultGID),
	}, nil
}

func attachToContainer(ctx context.Context, containerMgr *engine.ContainerManager, containerID string) error {
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
