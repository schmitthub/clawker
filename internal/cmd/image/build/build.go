// Package build provides the image build command.
package build

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/spf13/cobra"
)

// BuildOptions contains the options for the build command.
type BuildOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() *config.Config
	Client    func(context.Context) (*docker.Client, error)

	File      string   // -f, --file (Dockerfile path)
	Tags      []string // -t, --tag (multiple allowed)
	NoCache   bool     // --no-cache
	Pull      bool     // --pull
	BuildArgs []string // --build-arg KEY=VALUE
	Labels    []string // --label KEY=VALUE (user labels)
	Target    string   // --target
	Quiet     bool     // -q, --quiet
	Progress  string   // --progress (output formatting)
	Network   string   // --network
}

// NewCmdBuild creates the image build command.
func NewCmdBuild(f *cmdutil.Factory, runF func(context.Context, *BuildOptions) error) *cobra.Command {
	opts := &BuildOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Client:    f.Client,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return buildRun(cmd.Context(), opts)
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

	return cmd
}

func buildRun(ctx context.Context, opts *BuildOptions) error {
	ctx, cancel := term.SetupSignalContext(ctx)
	defer cancel()

	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Get configuration
	cfgGateway := opts.Config()
	cfg := cfgGateway.Project

	// Get working directory from project root, or fall back to current directory
	wd := cfg.RootDir()
	if wd == "" {
		var wdErr error
		wd, wdErr = os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("failed to get working directory: %w", wdErr)
		}
	}

	// Validate configuration
	validator := config.NewValidator(wd)
	if err := validator.Validate(cfg); err != nil {
		cmdutil.PrintError(ios, "Configuration validation failed")
		fmt.Fprintln(ios.ErrOut, err)
		return err
	}

	// Print any warnings
	for _, warning := range validator.Warnings() {
		cmdutil.PrintWarning(ios, "%s", warning)
	}

	// Handle Dockerfile path from -f/--file flag
	if opts.File != "" {
		cfg.Build.Dockerfile = opts.File
	}

	logger.Debug().
		Str("project", cfg.Project).
		Bool("no-cache", opts.NoCache).
		Bool("pull", opts.Pull).
		Str("target", opts.Target).
		Bool("quiet", opts.Quiet).
		Msg("starting build")

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Check BuildKit availability — cache mounts in Dockerfile require it
	var buildkitEnabled bool
	buildkitEnabled, bkErr := docker.BuildKitEnabled(ctx, client.APIClient)
	if bkErr != nil {
		logger.Warn().Err(bkErr).Msg("BuildKit detection failed")
	} else if !buildkitEnabled {
		cmdutil.PrintWarning(ios, "BuildKit is not available — cache mount directives will be ignored and builds may be slower\n")
	}

	// Determine image tag(s)
	imageTag := docker.ImageTag(cfg.Project)

	// Parse build args
	buildArgs := parseBuildArgs(opts.BuildArgs)

	// Merge user labels with clawker labels (clawker labels take precedence)
	userLabels := parseKeyValuePairs(opts.Labels)
	clawkerLabels := docker.ImageLabels(cfg.Project, cfg.Version)
	labels := mergeLabels(userLabels, clawkerLabels)

	builder := docker.NewBuilder(client, cfg, wd)

	logger.Info().
		Str("project", cfg.Project).
		Str("image", imageTag).
		Msg("building container image")

	// Build with options
	buildOpts := docker.BuilderOptions{
		NoCache:         opts.NoCache,
		Labels:          labels,
		Target:          opts.Target,
		Pull:            opts.Pull,
		SuppressOutput:  opts.Quiet || opts.Progress == "none",
		NetworkMode:     opts.Network,
		BuildArgs:       buildArgs,
		Tags:            opts.Tags,
		BuildKitEnabled: buildkitEnabled,
	}

	if err := builder.Build(ctx, imageTag, buildOpts); err != nil {
		cmdutil.HandleError(ios, err)
		cmdutil.PrintNextSteps(ios,
			"Check your Dockerfile for syntax errors",
			"Ensure the base image exists and is accessible",
			"Run 'clawker build --no-cache' to rebuild from scratch",
			"Use '--progress=plain' for detailed build output",
		)
		return err
	}

	if !opts.Quiet {
		if len(opts.Tags) > 0 {
			allTags := append([]string{imageTag}, opts.Tags...)
			fmt.Fprintf(ios.ErrOut, "%s Built image with tags: %s\n", cs.SuccessIcon(), strings.Join(allTags, ", "))
		} else {
			fmt.Fprintf(ios.ErrOut, "%s Built image: %s\n", cs.SuccessIcon(), imageTag)
		}
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
// Labels without '=' are logged as warnings and ignored.
func parseKeyValuePairs(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	result := make(map[string]string)
	var warnings []string
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		} else {
			warnings = append(warnings, pair)
		}
	}
	if len(warnings) > 0 {
		logger.Warn().
			Strs("invalid_labels", warnings).
			Msg("labels without '=' were ignored, use format KEY=VALUE")
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
