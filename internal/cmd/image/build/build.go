// Package build provides the image build command.
package build

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
)

// BuildOptions contains the options for the build command.
type BuildOptions struct {
	IOStreams       *iostreams.IOStreams
	TUI             *tui.TUI
	Config          func() (config.Config, error)
	Logger          func() (*logger.Logger, error)
	Client          func(context.Context) (*docker.Client, error)
	ProjectManager  func() (project.ProjectManager, error)
	ProjectRegistry func() (*project.Registry, error)
	HttpClient      func() (*http.Client, error)

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
	IIDFile   string   // --iidfile (write built image ID/digest to file)
}

// NewCmdBuild creates the image build command.
func NewCmdBuild(f *cmdutil.Factory, runF func(context.Context, *BuildOptions) error) *cobra.Command {
	opts := &BuildOptions{
		IOStreams:       f.IOStreams,
		TUI:             f.TUI,
		Config:          f.Config,
		Logger:          f.Logger,
		Client:          f.Client,
		ProjectManager:  f.ProjectManager,
		ProjectRegistry: f.ProjectRegistry,
		HttpClient:      f.HttpClient,
	}

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the project image",
		Long: `Build the project image from its clawker configuration.

Tags are harness-keyed: -t NAME builds that registered harness; -t name:NAME
adds an extra ref (tag part must name a registered harness). No -t builds the
default harness and adds the :default alias.`,
		Example: `  # Build the default harness image
  clawker image build

  # Build a specific registered harness
  clawker image build -t codex

  # Rebuild from scratch
  clawker image build --no-cache`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return buildRun(cmd.Context(), opts)
		},
	}

	// Docker CLI-compatible flags
	cmd.Flags().StringVarP(&opts.File, "file", "f", "", "Path to Dockerfile (overrides build.dockerfile in config)")
	cmd.Flags().
		StringArrayVarP(&opts.Tags, "tag", "t", nil, "Registered harness to build, or an extra ref whose tag names one (format: HARNESS or name:HARNESS)")
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Do not use cache when building the image")
	cmd.Flags().BoolVar(&opts.Pull, "pull", false, "Always attempt to pull a newer version of the base image")
	cmd.Flags().StringArrayVar(&opts.BuildArgs, "build-arg", nil, "Set build-time variables (format: KEY=VALUE)")
	cmd.Flags().StringArrayVar(&opts.Labels, "label", nil, "Set metadata for the image (format: KEY=VALUE)")
	cmd.Flags().StringVar(&opts.Target, "target", "", "Set the target build stage to build")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress the build output")
	cmd.Flags().StringVar(&opts.Progress, "progress", "auto", "Set type of progress output (auto, plain, tty, none)")
	cmd.Flags().StringVar(&opts.Network, "network", "", "Set the networking mode for the RUN instructions during build")
	cmd.Flags().
		StringVar(&opts.IIDFile, "iidfile", "", "Write the built image's ID/digest to this file (docker buildx --iidfile shape)")

	return cmd
}

func buildRun(ctx context.Context, opts *BuildOptions) error {
	ctx, cancel := signals.SetupSignalContext(ctx)
	defer cancel()

	ios := opts.IOStreams

	// Ensure CLI auth material on disk. The CLI is root of trust for its
	// own crypto (CA, signing key, server cert, client cert); the firewall
	// CA baked into this image also lands on disk here. Idempotent —
	// no-op on subsequent builds.
	if err := auth.EnsureAuthMaterial(); err != nil {
		return fmt.Errorf("ensure auth material: %w", err)
	}

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	suppressed := opts.Quiet || opts.Progress == "none"

	// Get configuration
	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg := cfgGateway.Project()

	// Resolve project name from ProjectManager
	var projectName string
	if opts.ProjectManager != nil {
		if pm, pmErr := opts.ProjectManager(); pmErr == nil {
			if p, pErr := pm.CurrentProject(ctx); pErr == nil {
				projectName = p.Name()
			}
		}
	}

	// Get working directory from the registry-resolved project root, or fall
	// back to current directory. ErrNotInProject is the normal "no registered
	// project" condition; any other error is a real registry/storage failure
	// and is surfaced rather than silently overwritten by the fallback.
	if opts.ProjectRegistry == nil {
		return fmt.Errorf("project registry not available")
	}
	reg, regErr := opts.ProjectRegistry()
	if regErr != nil {
		return fmt.Errorf("loading project registry: %w", regErr)
	}
	wd, wdErr := reg.CurrentRoot()
	if wdErr != nil && !errors.Is(wdErr, project.ErrNotInProject) {
		return fmt.Errorf("resolving project root: %w", wdErr)
	}
	if wd == "" {
		wd, wdErr = os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("failed to get working directory: %w", wdErr)
		}
	}

	cs := ios.ColorScheme()

	// Handle Dockerfile path from -f/--file flag
	if opts.File != "" {
		cfg.Build.Dockerfile = opts.File
	}

	// Early guard: no build image and no custom Dockerfile means nothing to build
	if cfg.Build.Image == "" && cfg.Build.Dockerfile == "" {
		return fmt.Errorf("%w", bundler.ErrNoBuildImage)
	}

	log.Debug().
		Str("project", projectName).
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
		log.Warn().Err(bkErr).Msg("BuildKit detection failed")
		fmt.Fprintf(ios.ErrOut, "%s BuildKit detection failed — falling back to legacy builder\n", cs.WarningIcon())
	} else if !buildkitEnabled {
		fmt.Fprintf(
			ios.ErrOut,
			"%s BuildKit is not available — cache mount directives will be ignored and builds may be slower\n",
			cs.WarningIcon(),
		)
	}

	// Parse build args
	buildArgs := parseBuildArgs(opts.BuildArgs)

	// Parse user labels from --label flags (clawker labels are added by the builder)
	userLabels, invalidLabels := parseKeyValuePairs(opts.Labels)
	if len(invalidLabels) > 0 {
		return cmdutil.FlagErrorf("malformed --label %q — use format KEY=VALUE", invalidLabels[0])
	}

	builder := docker.NewBuilder(client, cfg, wd, projectName)

	// Ensure shipped harness bundles exist on disk and in the settings
	// registry (both copy-if-missing — user edits win), then load the
	// selected bundle. The bundle's template blocks and context files
	// compose the rendered Dockerfile; its manifest drives version
	// resolution below.
	if ensureErr := bundler.EnsureHarnesses(cfgGateway); ensureErr != nil {
		log.Warn().
			Err(ensureErr).
			Msg("harness ensure failed — falling back to embedded bundles and built-in default")
	}

	// -t selects the harness: a bare NAME names a registered harness; a full
	// REF's tag part must equal one (strict tag=harness). No -t builds the
	// registry default.
	selector, extraTags, err := harnessSelectorFromTags(cfgGateway, opts.Tags)
	if err != nil {
		return err
	}
	harnessName, err := bundler.ResolveHarnessName(cfgGateway, selector)
	if err != nil {
		return fmt.Errorf("resolving harness: %w", err)
	}
	bundle, err := bundler.LoadHarness(cfgGateway, harnessName)
	if err != nil {
		return fmt.Errorf("loading harness %q: %w", harnessName, err)
	}

	// The canonical tag is harness-keyed; the registry default also gets the
	// :default alias so run/create resolve it without naming a harness.
	imageTag := docker.HarnessImageTag(projectName, harnessName)
	if isDefaultHarness(cfgGateway, harnessName) {
		extraTags = append(extraTags, docker.DefaultAliasImageTag(projectName))
	}

	// Resolve the harness's floating version to a concrete one once per
	// build, per the bundle manifest's version spec (e.g. the npm "latest"
	// dist-tag). The resolved value flows into the rendered Dockerfile's
	// version ARG default so the install layer's cache busts iff a new
	// release is published. Resolution failure (offline, registry down) is
	// non-fatal: warn and fall back to the floating literal — install RUN
	// still works, cache just doesn't auto-bust until the next online build.
	harnessVersion := bundler.DefaultHarnessVersion
	httpClient, err := opts.HttpClient()
	if err != nil {
		log.Warn().
			Err(err).
			Msg("HTTP client initialization failed — cannot resolve latest harness version, install layer cache will not bust until next online build")
	} else if resolved, resErr := bundler.ResolveHarnessVersion(ctx, httpClient, bundle); resErr != nil {
		log.Warn().
			Err(resErr).
			Msg("harness version resolution failed — install layer cache will not bust until next online build")
		fmt.Fprintf(
			ios.ErrOut,
			"%s Could not resolve latest %s version (%v) — using %q literal; cache will not bust on a new release until network returns\n",
			cs.WarningIcon(),
			harnessName,
			resErr,
			bundler.DefaultHarnessVersion,
		)
	} else {
		// Assign to the outer var so the resolved version actually reaches the
		// build ARG below — a `:=` here would shadow and silently drop it.
		harnessVersion = resolved
		log.Debug().
			Str("harness", harnessName).
			Str("harness_version", harnessVersion).
			Msg("resolved harness version for ARG default")
	}
	// Build options. OnComplete stashes the digest into a closure variable;
	// post-build handling (success log, --iidfile write) runs in the main
	// goroutine after builder.Build returns so write/empty-digest errors
	// surface through the normal error-return path instead of being
	// swallowed inside the build goroutine.
	var imageDigest string
	log.Debug().
		Str("project", projectName).
		Str("image", imageTag).
		Msg("building container image")
	buildOpts := docker.BuilderOptions{
		NoCache:         opts.NoCache,
		Labels:          userLabels,
		Target:          opts.Target,
		Pull:            opts.Pull,
		SuppressOutput:  suppressed,
		NetworkMode:     opts.Network,
		BuildArgs:       buildArgs,
		Tags:            extraTags,
		BuildKitEnabled: buildkitEnabled,
		HarnessVersion:  harnessVersion,
		HarnessName:     harnessName,
		OnComplete: func(res whail.BuildResult) {
			imageDigest = res.ImageID
		},
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
			Title:          "Building " + projectName,
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

		// Always drain the build goroutine before deciding which error to
		// surface. The goroutine sends to buildErrCh before closing ch, so
		// by the time RunProgress returns buildErrCh is ready to read.
		// Without this drain a TUI render error would mask a real build
		// failure (e.g. both triggered by ctx cancel).
		buildErr := <-buildErrCh

		if buildErr != nil {
			if result.Err != nil {
				log.Warn().Err(result.Err).Msg("progress display error masked by build error")
			}
			printBuildNextSteps(ios, cs)
			return buildErr
		}
		if result.Err != nil {
			printBuildNextSteps(ios, cs)
			return result.Err
		}
		return finishBuild(log, imageTag, imageDigest, opts.IIDFile)
	}

	// Suppressed output — build synchronously without progress display.
	if err := builder.Build(ctx, imageTag, buildOpts); err != nil {
		printBuildNextSteps(ios, cs)
		return err
	}
	return finishBuild(log, imageTag, imageDigest, opts.IIDFile)
}

// finishBuild logs build success and, when --iidfile is set, writes the
// resolved image digest to the named file. Returns a hard error when the
// user requested an --iidfile but the builder returned no digest, or when
// the file write itself fails — both are CI-breaking footguns to swallow.
func finishBuild(log *logger.Logger, imageTag, imageDigest, iidFile string) error {
	log.Info().
		Str("image", imageTag).
		Str("image_id", imageDigest).
		Msg("image build complete")
	if iidFile == "" {
		return nil
	}
	if imageDigest == "" {
		return fmt.Errorf("--iidfile %q requested but builder returned no image digest", iidFile)
	}
	if err := os.WriteFile(iidFile, []byte(imageDigest), 0o644); err != nil {
		return fmt.Errorf("write --iidfile %q: %w", iidFile, err)
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

// harnessSelectorFromTags interprets -t entries under the strict
// tag=harness scheme. A bare NAME selects that harness; a full REF (has a
// colon) keeps riding as an additional image tag, but its tag part must
// name a registered harness. All entries must agree on one harness.
func harnessSelectorFromTags(cfg config.Config, tags []string) (string, []string, error) {
	var selector string
	var extraTags []string
	for _, entry := range tags {
		candidate := entry
		fullRef := false
		if idx := strings.LastIndex(entry, ":"); idx >= 0 {
			candidate = entry[idx+1:]
			fullRef = true
		}
		if !isKnownHarness(cfg, candidate) {
			//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
			return "", nil, cmdutil.FlagErrorf(
				"--tag %q does not name a registered harness (tags are harness-keyed; registered: %s)",
				entry, strings.Join(registeredHarnessNames(cfg), ", "),
			)
		}
		if selector != "" && selector != candidate {
			//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
			return "", nil, cmdutil.FlagErrorf(
				"--tag entries select conflicting harnesses (%s vs %s) — one harness per build",
				selector, candidate,
			)
		}
		selector = candidate
		if fullRef {
			extraTags = append(extraTags, entry)
		}
	}
	return selector, extraTags, nil
}

// registeredHarnessNames lists registry keys, falling back to the shipped
// set when no registry exists yet (bootstrap).
func registeredHarnessNames(cfg config.Config) []string {
	if s := cfg.Settings(); s != nil && len(s.Harnesses) > 0 {
		names := make([]string, 0, len(s.Harnesses))
		for name := range s.Harnesses {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}
	return bundler.ShippedHarnessNames()
}

// isKnownHarness reports whether name is a registry key (or a shipped name
// during bootstrap, before any registry exists).
func isKnownHarness(cfg config.Config, name string) bool {
	return slices.Contains(registeredHarnessNames(cfg), name)
}

// isDefaultHarness reports whether name is the registry-default harness —
// the one whose image carries the :default alias tag.
func isDefaultHarness(cfg config.Config, name string) bool {
	resolved, err := bundler.ResolveHarnessName(cfg, "")
	return err == nil && resolved == name
}
