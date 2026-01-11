package build

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/claucker/internal/build"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// BuildOptions contains the options for the build command.
type BuildOptions struct {
	NoCache    bool
	Dockerfile string
}

// NewCmdBuild creates a new build command.
func NewCmdBuild(f *cmdutil.Factory) *cobra.Command {
	opts := &BuildOptions{}

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the container image",
		Long: `Builds the container image for this project.

The image is always built unconditionally. Use --no-cache to build
without Docker's layer cache for a completely fresh build.`,
		Example: `  # Build image (uses Docker cache)
  claucker build

  # Build image without cache
  claucker build --no-cache

  # Build using custom Dockerfile
  claucker build --dockerfile ./Dockerfile.dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Build image without using Docker cache")
	cmd.Flags().StringVar(&opts.Dockerfile, "dockerfile", "", "Path to custom Dockerfile (overrides build.dockerfile in config)")

	return cmd
}

func runBuild(f *cmdutil.Factory, opts *BuildOptions) error {
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

	// Override dockerfile from CLI flag if provided
	if opts.Dockerfile != "" {
		cfg.Build.Dockerfile = opts.Dockerfile
	}

	logger.Debug().
		Str("project", cfg.Project).
		Bool("no-cache", opts.NoCache).
		Msg("starting build")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	// Build image
	imageTag := engine.ImageTag(cfg.Project)
	builder := build.NewBuilder(eng, cfg, f.WorkDir)

	logger.Info().
		Str("project", cfg.Project).
		Str("image", imageTag).
		Msg("building container image")

	if err := builder.Build(ctx, imageTag, opts.NoCache); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Successfully built image: %s\n", imageTag)
	return nil
}
