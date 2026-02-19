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
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/spf13/cobra"
)

// BuildOptions contains the options for the build command.
type BuildOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Config    func() (config.Config, error)
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
		TUI:       f.TUI,
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
	ctx, cancel := signals.SetupSignalContext(ctx)
	defer cancel()

	ios := opts.IOStreams

	suppressed := opts.Quiet || opts.Progress == "none"

	// Get configuration
	cfgGateway := opts.Config()
	cfg := cfgGateway.ProjectCfg()

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
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Print any warnings
	cs := ios.ColorScheme()
	for _, warning := range validator.Warnings() {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), warning)
	}

	// Handle Dockerfile path from -f/--file flag
	if opts.File != "" {
		cfg.Build.Dockerfile = opts.File
	}

	ios.Logger.Debug().
		Str("project", cfg.Project).
		Bool("no-cache", opts.NoCache).
		Bool("pull", opts.Pull).
		Str("target", opts.Target).
		Bool("quiet", opts.Quiet).
		Msg("starting build")

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return err
	}

	// Check BuildKit availability — cache mounts in Dockerfile require it
	buildkitEnabled, bkErr := docker.BuildKitEnabled(ctx, client.APIClient)
	if bkErr != nil {
		ios.Logger.Warn().Err(bkErr).Msg("BuildKit detection failed")
		fmt.Fprintf(ios.ErrOut, "%s BuildKit detection failed — falling back to legacy builder\n", cs.WarningIcon())
	} else if !buildkitEnabled {
		fmt.Fprintf(ios.ErrOut, "%s BuildKit is not available — cache mount directives will be ignored and builds may be slower\n", cs.WarningIcon())
	}

	// Determine image tag(s)
	imageTag := docker.ImageTag(cfg.Project)

	// Parse build args
	buildArgs := parseBuildArgs(opts.BuildArgs)

	// Merge user labels with clawker labels (clawker labels take precedence)
	userLabels, invalidLabels := parseKeyValuePairs(opts.Labels)
	for _, label := range invalidLabels {
		fmt.Fprintf(ios.ErrOut, "%s Ignoring malformed label %q — use format KEY=VALUE\n", cs.WarningIcon(), label)
	}
	clawkerLabels := docker.ImageLabels(cfg.Project, cfg.Version)
	labels := mergeLabels(userLabels, clawkerLabels)

	builder := docker.NewBuilder(client, cfg, wd)

	// Build with options.
	// Defense in depth: --no-cache should also skip content hash check if
	// EnsureImage() is ever used. This ensures explicit no-cache requests
	// always trigger a full rebuild.
	ios.Logger.Debug().
		Str("project", cfg.Project).
		Str("image", imageTag).
		Msg("building container image")
	buildOpts := docker.BuilderOptions{
		ForceBuild:      opts.NoCache,
		NoCache:         opts.NoCache,
		Labels:          labels,
		Target:          opts.Target,
		Pull:            opts.Pull,
		SuppressOutput:  suppressed,
		NetworkMode:     opts.Network,
		BuildArgs:       buildArgs,
		Tags:            opts.Tags,
		BuildKitEnabled: buildkitEnabled,
	}

	// Wire progress display when output is not suppressed.
	// The build runs in a goroutine with events streamed to a TUI display.
	if !suppressed {
		ch := make(chan tui.ProgressStep, 64)
		done := make(chan struct{})

		buildOpts.OnProgress = func(event whail.BuildProgressEvent) {
			select {
			case <-done:
				return // display already finished, discard late events
			case ch <- tui.ProgressStep{
				ID:      event.StepID,
				Name:    event.StepName,
				Status:  progressStatus(event.Status),
				Cached:  event.Cached,
				Error:   event.Error,
				LogLine: event.LogLine,
			}:
			}
		}

		buildErrCh := make(chan error, 1)
		go func() {
			buildErrCh <- builder.Build(ctx, imageTag, buildOpts)
			close(ch) // channel closure = done signal
		}()

		result := opts.TUI.RunProgress(opts.Progress, tui.ProgressDisplayConfig{
			Title:          "Building " + cfg.Project,
			Subtitle:       imageTag,
			CompletionVerb: "Built",
			MaxVisible:     5,
			LogLines:       3,
			IsInternal:     whail.IsInternalStep,
			CleanName:      whail.CleanStepName,
			ParseGroup:     whail.ParseBuildStage,
			FormatDuration: whail.FormatBuildDuration,
		}, ch)
		close(done) // signal OnProgress callback to stop sending

		if result.Err != nil {
			printBuildNextSteps(ios, cs)
			return result.Err
		}

		if buildErr := <-buildErrCh; buildErr != nil {
			printBuildNextSteps(ios, cs)
			return buildErr
		}

		return nil
	}

	// Suppressed output — build synchronously without progress display.
	if err := builder.Build(ctx, imageTag, buildOpts); err != nil {
		printBuildNextSteps(ios, cs)
		return err
	}

	return nil
}

// progressStatus converts a whail build step status to a tui progress step status.
// Explicit switch avoids iota alignment tricks between packages.
func progressStatus(s whail.BuildStepStatus) tui.ProgressStepStatus {
	switch s {
	case whail.BuildStepRunning:
		return tui.StepRunning
	case whail.BuildStepComplete:
		return tui.StepComplete
	case whail.BuildStepCached:
		return tui.StepCached
	case whail.BuildStepError:
		return tui.StepError
	default:
		return tui.StepPending
	}
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
// Labels without '=' are returned as invalid so the caller can warn.
func parseKeyValuePairs(pairs []string) (map[string]string, []string) {
	if len(pairs) == 0 {
		return nil, nil
	}
	result := make(map[string]string)
	var invalid []string
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		} else {
			invalid = append(invalid, pair)
		}
	}
	return result, invalid
}

// printBuildNextSteps prints actionable guidance after a build failure.
func printBuildNextSteps(ios *iostreams.IOStreams, cs *iostreams.ColorScheme) {
	fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
	fmt.Fprintln(ios.ErrOut, "  1. Check your Dockerfile for syntax errors")
	fmt.Fprintln(ios.ErrOut, "  2. Ensure the base image exists and is accessible")
	fmt.Fprintln(ios.ErrOut, "  3. Run 'clawker build --no-cache' to rebuild from scratch")
	fmt.Fprintln(ios.ErrOut, "  4. Use '--progress=plain' for detailed build output")
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
