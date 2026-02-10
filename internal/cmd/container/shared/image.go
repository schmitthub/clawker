package shared

import (
	"context"
	"fmt"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
)

// TODO: This is implemented wrong. constructors need to be added to accept factory *cmdutil.Factory, we don't pass indivdual deps in options)

// RebuildMissingImageOpts holds options for the rebuild prompt flow.
// Uses DI closures (like InitConfigOpts) to avoid direct Factory coupling.
type RebuildMissingImageOpts struct {
	ImageRef       string
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	Prompter       func() *prompter.Prompter
	SettingsLoader func() config.SettingsLoader
	BuildImage     docker.BuildDefaultImageFn
	CommandVerb    string // "run" or "create" for error messages
}

// RebuildMissingDefaultImage prompts the user to rebuild a missing default image.
// In non-interactive mode, prints instructions and returns an error.
// In interactive mode, prompts for flavor selection and rebuilds with TUI progress.
func RebuildMissingDefaultImage(ctx context.Context, opts RebuildMissingImageOpts) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	if !ios.IsInteractive() {
		fmt.Fprintf(ios.ErrOut, "%s Default image %q not found\n", cs.FailureIcon(), opts.ImageRef)
		printImageNotFoundNextSteps(ios, cs, opts.CommandVerb)
		return fmt.Errorf("default image %q not found", opts.ImageRef)
	}

	// Interactive mode — prompt to rebuild
	p := opts.Prompter()
	selectOpts := []prompter.SelectOption{
		{Label: "Yes", Description: "Rebuild the default base image now"},
		{Label: "No", Description: "Cancel and fix manually"},
	}

	idx, err := p.Select(
		fmt.Sprintf("Default image %q not found. Rebuild now?", opts.ImageRef),
		selectOpts,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to prompt for rebuild: %w", err)
	}

	if idx != 0 {
		printImageNotFoundNextSteps(ios, cs, opts.CommandVerb)
		return fmt.Errorf("default image %q not found", opts.ImageRef)
	}

	// Get flavor selection
	flavors := intbuild.DefaultFlavorOptions()
	flavorOptions := make([]prompter.SelectOption, len(flavors))
	for i, f := range flavors {
		flavorOptions[i] = prompter.SelectOption{
			Label:       f.Name,
			Description: f.Description,
		}
	}

	flavorIdx, err := p.Select("Select Linux flavor", flavorOptions, 0)
	if err != nil {
		return fmt.Errorf("failed to select flavor: %w", err)
	}

	selectedFlavor := flavors[flavorIdx].Name

	// Build with TUI progress display if available, else spinner fallback
	if err := buildWithProgress(ctx, opts, selectedFlavor); err != nil {
		return fmt.Errorf("failed to rebuild default image: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "%s Using image: %s\n", cs.SuccessIcon(), docker.DefaultImageTag)

	// Persist the default image in settings
	persistDefaultImageSetting(opts.SettingsLoader)

	return nil
}

// buildWithProgress builds the default image with TUI progress display if available,
// or falls back to a spinner when TUI is nil (tests, non-TTY).
func buildWithProgress(ctx context.Context, opts RebuildMissingImageOpts, flavor string) error {
	if opts.TUI != nil {
		return buildWithTUIProgress(ctx, opts, flavor)
	}

	// Fallback: simple spinner
	return opts.IOStreams.RunWithSpinner(
		fmt.Sprintf("Building %s", docker.DefaultImageTag),
		func() error {
			return opts.BuildImage(ctx, flavor, nil)
		},
	)
}

// buildWithTUIProgress wires the BuildDefaultImage progress callback to TUI.RunProgress.
// Follows the same pattern as internal/cmd/image/build/build.go.
func buildWithTUIProgress(ctx context.Context, opts RebuildMissingImageOpts, flavor string) error {
	ch := make(chan tui.ProgressStep, 64)
	done := make(chan struct{})

	onProgress := func(event whail.BuildProgressEvent) {
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
		buildErrCh <- opts.BuildImage(ctx, flavor, onProgress)
		close(ch) // channel closure = done signal
	}()

	result := opts.TUI.RunProgress("auto", tui.ProgressDisplayConfig{
		Title:          "Building default image",
		Subtitle:       docker.DefaultImageTag,
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
		return result.Err
	}
	if buildErr := <-buildErrCh; buildErr != nil {
		return buildErr
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

// printImageNotFoundNextSteps prints guidance when the default image is missing.
func printImageNotFoundNextSteps(ios *iostreams.IOStreams, cs *iostreams.ColorScheme, commandVerb string) {
	fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
	fmt.Fprintln(ios.ErrOut, "  1. Run 'clawker init' to rebuild the base image")
	fmt.Fprintf(ios.ErrOut, "  2. Or specify an image explicitly: clawker %s IMAGE\n", commandVerb)
	fmt.Fprintln(ios.ErrOut, "  3. Or build a project image: clawker build")
}

// persistDefaultImageSetting saves the default image tag in user settings.
func persistDefaultImageSetting(settingsLoaderFn func() config.SettingsLoader) {
	if settingsLoaderFn == nil {
		return
	}
	loader := settingsLoaderFn()
	if loader == nil {
		return
	}
	currentSettings, loadErr := loader.Load()
	if loadErr != nil {
		logger.Warn().Err(loadErr).Msg("failed to load existing settings; skipping settings update")
		return
	}
	currentSettings.DefaultImage = docker.DefaultImageTag
	if saveErr := loader.Save(currentSettings); saveErr != nil {
		logger.Warn().Err(saveErr).Msg("failed to update settings with default image")
	}
}
