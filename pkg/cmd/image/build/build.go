// Package build provides the image build command.
package build

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// BuildOptions contains the options for the build command.
type BuildOptions struct {
	File       string   // -f, --file (Dockerfile path)
	Tags       []string // -t, --tag (multiple allowed)
	NoCache    bool     // --no-cache
	Pull       bool     // --pull
	BuildArgs  []string // --build-arg KEY=VALUE
	Labels     []string // --label KEY=VALUE (user labels)
	Target     string   // --target
	Quiet      bool     // -q, --quiet
	Progress   string   // --progress (output formatting)
	Network    string   // --network
	Dockerfile string   // --dockerfile (deprecated, hidden)
}

// NewCmd creates the image build command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &BuildOptions{}

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build an image from a clawker project",
		Long: `Builds a container image from a clawker project configuration.

The image is built from the project's clawker.yaml configuration,
generating a Dockerfile and building the image. Alternatively,
use -f/--file to specify a custom Dockerfile.

Multiple tags can be applied to the built image using -t/--tag.
Build-time variables can be passed using --build-arg.`,
		Example: `  # Build the project image
  clawker image build

  # Build without Docker cache
  clawker image build --no-cache

  # Build using a custom Dockerfile
  clawker image build -f ./Dockerfile.dev

  # Build with multiple tags
  clawker image build -t myapp:latest -t myapp:v1.0

  # Build with build arguments
  clawker image build --build-arg NODE_VERSION=20

  # Build a specific target stage
  clawker image build --target builder

  # Build quietly (suppress output)
  clawker image build -q

  # Always pull base image
  clawker image build --pull`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(f, opts)
		},
	}

	// Docker CLI-compatible flags
	cmd.Flags().StringVarP(&opts.File, "file", "f", "", "Path to Dockerfile (overrides build.dockerfile in config)")
	cmd.Flags().StringArrayVarP(&opts.Tags, "tag", "t", nil, "Name and optionally a tag (format: name:tag)")
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Do not use cache when building the image")
	cmd.Flags().BoolVar(&opts.Pull, "pull", false, "Always attempt to pull a newer version of the base image")
	cmd.Flags().StringArrayVar(&opts.BuildArgs, "build-arg", nil, "Set build-time variables (format: KEY=VALUE)")
	cmd.Flags().StringArrayVar(&opts.Labels, "label", nil, "Set metadata for the image (format: KEY=VALUE)")
	cmd.Flags().StringVar(&opts.Target, "target", "", "Set the target build stage to build")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress the build output")
	cmd.Flags().StringVar(&opts.Progress, "progress", "auto", "Set type of progress output (auto, plain, tty, none)")
	cmd.Flags().StringVar(&opts.Network, "network", "", "Set the networking mode for the RUN instructions during build")

	// Deprecated flag for backward compatibility
	cmd.Flags().StringVar(&opts.Dockerfile, "dockerfile", "", "Path to custom Dockerfile (deprecated: use -f/--file)")
	_ = cmd.Flags().MarkHidden("dockerfile")
	_ = cmd.Flags().MarkDeprecated("dockerfile", "use -f/--file instead")

	return cmd
}

func runBuild(f *cmdutil.Factory, opts *BuildOptions) error {
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

	// Handle Dockerfile path (prefer -f/--file, fall back to deprecated --dockerfile)
	dockerfilePath := opts.File
	if dockerfilePath == "" && opts.Dockerfile != "" {
		dockerfilePath = opts.Dockerfile
	}
	if dockerfilePath != "" {
		cfg.Build.Dockerfile = dockerfilePath
	}

	logger.Debug().
		Str("project", cfg.Project).
		Bool("no-cache", opts.NoCache).
		Bool("pull", opts.Pull).
		Str("target", opts.Target).
		Bool("quiet", opts.Quiet).
		Msg("starting build")

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Determine image tag(s)
	imageTag := docker.ImageTag(cfg.Project)

	// Parse build args
	buildArgs := parseBuildArgs(opts.BuildArgs)

	// Merge user labels with clawker labels (clawker labels take precedence)
	userLabels := parseKeyValuePairs(opts.Labels)
	clawkerLabels := docker.ImageLabels(cfg.Project, cfg.Version)
	labels := mergeLabels(userLabels, clawkerLabels)

	builder := build.NewBuilder(client, cfg, f.WorkDir)

	logger.Info().
		Str("project", cfg.Project).
		Str("image", imageTag).
		Msg("building container image")

	// Build with options
	buildOpts := build.Options{
		NoCache:        opts.NoCache,
		Labels:         labels,
		Target:         opts.Target,
		Pull:           opts.Pull,
		SuppressOutput: opts.Quiet || opts.Progress == "none",
		NetworkMode:    opts.Network,
		BuildArgs:      buildArgs,
	}

	if err := builder.Build(ctx, imageTag, buildOpts); err != nil {
		return err
	}

	if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "Successfully built image: %s\n", imageTag)
	}
	return nil
}

// parseBuildArgs parses KEY=VALUE build arguments into a map.
func parseBuildArgs(args []string) map[string]*string {
	if len(args) == 0 {
		return nil
	}
	result := make(map[string]*string)
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) == 2 {
			value := parts[1]
			result[parts[0]] = &value
		} else if len(parts) == 1 {
			// Allow KEY without value (uses env var)
			result[parts[0]] = nil
		}
	}
	return result
}

// parseKeyValuePairs parses KEY=VALUE pairs into a string map.
func parseKeyValuePairs(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	result := make(map[string]string)
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

// mergeLabels merges user labels with clawker labels.
// Clawker labels take precedence over user labels.
func mergeLabels(userLabels, clawkerLabels map[string]string) map[string]string {
	result := make(map[string]string)

	// Add user labels first
	for k, v := range userLabels {
		result[k] = v
	}

	// Clawker labels override user labels
	for k, v := range clawkerLabels {
		result[k] = v
	}

	return result
}
