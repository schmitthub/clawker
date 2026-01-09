package up

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

// UpOptions contains the options for the up command.
type UpOptions struct {
	Mode   string
	Build  bool
	Detach bool
	Clean  bool
}

// NewCmdUp creates the up command.
func NewCmdUp(f *cmdutil.Factory) *cobra.Command {
	opts := &UpOptions{}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Build and run Claude in a container",
		Long: `Builds the container image (if needed), creates volumes, and runs Claude.

This is an idempotent operation:
  - If a container is already running, attaches to it
  - If a container exists but is stopped, starts and attaches
  - If no container exists, creates and starts one

Workspace modes:
  --mode=bind      Live sync with host filesystem (default)
  --mode=snapshot  Copy files to ephemeral Docker volume`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Mode, "mode", "m", "", "Workspace mode: bind or snapshot (default from config)")
	cmd.Flags().BoolVar(&opts.Build, "build", false, "Force rebuild of the container image")
	cmd.Flags().BoolVar(&opts.Detach, "detach", false, "Run container in background (detached mode)")
	cmd.Flags().BoolVar(&opts.Clean, "clean", false, "Remove existing container and volumes before starting")

	return cmd
}

func runUp(f *cmdutil.Factory, opts *UpOptions) error {
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
		Bool("build", opts.Build).
		Bool("detach", opts.Detach).
		Msg("starting up")

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

	// Clean if requested
	if opts.Clean {
		if err := cleanupResources(ctx, eng, cfg.Project); err != nil {
			logger.Warn().Err(err).Msg("cleanup encountered errors")
		}
	}

	// Determine workspace mode
	mode, err := determineMode(cfg, opts.Mode)
	if err != nil {
		return err
	}

	logger.Info().
		Str("project", cfg.Project).
		Str("mode", string(mode)).
		Msg("starting Claude container")

	// Build or ensure image
	imageTag := engine.ImageTag(cfg.Project)
	if err := ensureImage(ctx, eng, cfg, imageTag, f.WorkDir, opts.Build); err != nil {
		return err
	}

	// Setup workspace strategy
	wsStrategy, err := setupWorkspace(ctx, eng, cfg, mode, f.WorkDir)
	if err != nil {
		return err
	}

	// Build container configuration
	containerCfg, err := buildContainerConfig(cfg, imageTag, wsStrategy, f.WorkDir)
	if err != nil {
		return err
	}

	// Create or find container
	containerMgr := engine.NewContainerManager(eng)
	containerID, created, err := containerMgr.FindOrCreate(containerCfg)
	if err != nil {
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		}
		return err
	}

	if created {
		logger.Info().
			Str("container", engine.ContainerName(cfg.Project)).
			Str("mode", string(mode)).
			Msg("created new container")
	} else {
		logger.Info().
			Str("container", engine.ContainerName(cfg.Project)).
			Msg("using existing container")
	}

	// Handle detached mode
	if opts.Detach {
		fmt.Printf("Container %s is running in detached mode\n", engine.ContainerName(cfg.Project))
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  claucker logs      # View container logs")
		fmt.Println("  claucker sh        # Open shell in container")
		fmt.Println("  claucker down      # Stop the container")
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

func ensureImage(ctx context.Context, eng *engine.Engine, cfg *config.Config, imageTag, workDir string, forceBuild bool) error {
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

		return imgMgr.BuildImage(buildCtx, imageTag, filepath.Base(gen.GetCustomDockerfilePath()), nil)
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

	return imgMgr.BuildImage(buildCtx, imageTag, "Dockerfile", nil)
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

func buildContainerConfig(cfg *config.Config, imageTag string, wsStrategy workspace.Strategy, workDir string) (engine.ContainerConfig, error) {
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

	return engine.ContainerConfig{
		Name:         engine.ContainerName(cfg.Project),
		Image:        imageTag,
		Mounts:       mounts,
		Env:          envBuilder.Build(),
		WorkingDir:   cfg.Workspace.RemotePath,
		Tty:          true,
		OpenStdin:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		CapAdd:       capAdd,
		User:         fmt.Sprintf("%d:%d", dockerfile.DefaultUID, dockerfile.DefaultGID),
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
		os.Exit(int(exitCode))
	}

	return nil
}

func cleanupResources(ctx context.Context, eng *engine.Engine, projectName string) error {
	containerMgr := engine.NewContainerManager(eng)
	volumeMgr := engine.NewVolumeManager(eng)

	// Remove container
	containerName := engine.ContainerName(projectName)
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
		engine.VolumeName(projectName, "workspace"),
		engine.VolumeName(projectName, "config"),
		engine.VolumeName(projectName, "history"),
	}

	if err := volumeMgr.RemoveVolumes(volumes, true); err != nil {
		logger.Warn().Err(err).Msg("failed to remove some volumes")
	}

	return nil
}
