package init

import (
	"context"
	"fmt"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// InitOptions contains the options for the init command.
type InitOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Prompter  func() *prompterpkg.Prompter
	Config    func() config.Provider
	Client    func(context.Context) (*docker.Client, error)

	Yes bool // Non-interactive mode
}

// NewCmdInit creates the init command for user-level setup.
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command {
	opts := &InitOptions{
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Prompter:  f.Prompter,
		Config:    f.Config,
		Client:    f.Client,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize clawker user settings",
		Long: `Creates or updates the user settings file at ~/.local/clawker/settings.yaml.

This command sets up user-level defaults that apply across all clawker projects.
In interactive mode (default), you will be prompted to:
  - Build an initial base image (recommended)
  - Select a Linux flavor (Debian or Alpine)

Use --yes/-y to skip prompts and accept all defaults (skips base image build).

To initialize a project in the current directory, use 'clawker project init' instead.`,
		Example: `  # Interactive setup (prompts for options)
  clawker init

  # Non-interactive with all defaults
  clawker init --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

// Run executes the init command logic.
func Run(ctx context.Context, opts *InitOptions) error {
	if opts.Yes || !opts.IOStreams.IsInteractive() {
		return runNonInteractive(ctx, opts)
	}
	return runInteractive(ctx, opts)
}

// runInteractive runs the wizard-based interactive flow.
func runInteractive(ctx context.Context, opts *InitOptions) error {
	fields := buildWizardFields()
	result, err := opts.TUI.RunWizard(fields)
	if err != nil {
		return fmt.Errorf("wizard failed: %w", err)
	}
	if !result.Submitted {
		fmt.Fprintln(opts.IOStreams.ErrOut, "Setup cancelled.")
		return nil
	}

	buildImage := result.Values["build"] == "Yes"
	flavor := result.Values["flavor"]
	if result.Values["confirm"] != "yes" {
		return nil // user declined at submit step
	}

	return performSetup(ctx, opts, buildImage, flavor)
}

// runNonInteractive runs the non-interactive (--yes) path with no prompts.
func runNonInteractive(ctx context.Context, opts *InitOptions) error {
	return performSetup(ctx, opts, false, "")
}

// buildWizardFields returns the wizard field definitions for interactive init.
func buildWizardFields() []tui.WizardField {
	return []tui.WizardField{
		{
			ID:     "build",
			Title:  "Build Image",
			Prompt: "Build an initial base image?",
			Kind:   tui.FieldSelect,
			Options: []tui.FieldOption{
				{Label: "Yes", Description: "Build a clawker-optimized base image (Recommended)"},
				{Label: "No", Description: "Skip - specify images per-project later"},
			},
			DefaultIdx: 0,
		},
		{
			ID:         "flavor",
			Title:      "Flavor",
			Prompt:     "Select Linux flavor",
			Kind:       tui.FieldSelect,
			Options:    flavorFieldOptions(),
			DefaultIdx: 0,
			SkipIf: func(vals tui.WizardValues) bool {
				return vals["build"] != "Yes"
			},
		},
		{
			ID:         "confirm",
			Title:      "Submit",
			Prompt:     "Proceed with setup?",
			Kind:       tui.FieldConfirm,
			DefaultYes: true,
		},
	}
}

// flavorFieldOptions converts bundler flavor options to TUI wizard field options.
func flavorFieldOptions() []tui.FieldOption {
	flavors := intbuild.DefaultFlavorOptions()
	options := make([]tui.FieldOption, len(flavors))
	for i, f := range flavors {
		options[i] = tui.FieldOption{
			Label:       f.Name,
			Description: f.Description,
		}
	}
	return options
}

// performSetup handles the actual settings save and optional base image build.
// Both runInteractive and runNonInteractive delegate to this function.
func performSetup(ctx context.Context, opts *InitOptions, buildBaseImage bool, selectedFlavor string) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Print header
	fmt.Fprintln(ios.ErrOut, "Setting up clawker user settings...")
	fmt.Fprintln(ios.ErrOut)

	// Get settings loader from Config gateway
	cfg := opts.Config()
	settingsLoader := cfg.SettingsLoader()
	if settingsLoader == nil {
		// Fallback: create a settings loader directly (e.g. first run, no config loaded yet)
		ios.Logger.Warn().Msg("SettingsLoader not set on Config; creating filesystem loader as fallback")
		var err error
		settingsLoader, err = config.NewSettingsLoader()
		if err != nil {
			return fmt.Errorf("failed to create settings loader: %w", err)
		}
		cfg.SetSettingsLoader(settingsLoader)
	}

	// Load existing settings or create defaults
	settings := cfg.UserSettings()
	if settings == nil {
		settings = config.DefaultSettings()
	}

	if buildBaseImage {
		// Clear default image when building (will be set after successful build)
		settings.DefaultImage = ""
	}

	ios.Logger.Debug().
		Bool("build_base_image", buildBaseImage).
		Str("flavor", selectedFlavor).
		Msg("initializing user settings")

	// Save initial settings
	if err := settingsLoader.Save(settings); err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}

	ios.Logger.Debug().Str("file", settingsLoader.Path()).Msg("saved user settings")

	// Success output
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), settingsLoader.Path())

	// Ensure shared directory exists on host for bind-mounting into containers
	shareDir, err := config.ShareDir()
	if err != nil {
		return fmt.Errorf("failed to resolve share directory: %w", err)
	}
	if err := config.EnsureDir(shareDir); err != nil {
		return fmt.Errorf("failed to create share directory: %w", err)
	}
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), shareDir)

	// Build base image with TUI progress display
	if buildBaseImage {
		fmt.Fprintln(ios.ErrOut)

		baseImage := intbuild.FlavorToImage(selectedFlavor)
		project := &config.Project{
			Version: "1",
			Build: config.BuildConfig{
				Image: baseImage,
			},
		}

		initCfg := &config.Config{
			Project:  project,
			Settings: cfg.UserSettings(), // effective settings from the gateway
		}
		gen := intbuild.NewProjectGenerator(initCfg, "")
		buildContext, err := gen.GenerateBuildContext()
		if err != nil {
			return fmt.Errorf("failed to generate build context: %w", err)
		}

		client, err := opts.Client(ctx)
		if err != nil {
			return fmt.Errorf("failed to create docker client: %w", err)
		}
		defer client.Close()

		// Use TUI RunProgress for the build
		ch := make(chan tui.ProgressStep, 4)
		buildErrCh := make(chan error, 1)

		go func() {
			defer close(ch)
			ch <- tui.ProgressStep{
				ID:     "build",
				Name:   "Building base image (" + selectedFlavor + ")",
				Status: tui.StepRunning,
			}
			err := client.BuildImage(ctx, buildContext, docker.BuildImageOpts{
				Tags:           []string{docker.DefaultImageTag},
				SuppressOutput: true,
				Labels: map[string]string{
					docker.LabelManaged:   docker.ManagedLabelValue,
					docker.LabelBaseImage: docker.ManagedLabelValue,
					docker.LabelFlavor:    selectedFlavor,
				},
			})
			if err != nil {
				ch <- tui.ProgressStep{
					ID:     "build",
					Status: tui.StepError,
					Error:  err.Error(),
				}
				buildErrCh <- err
				return
			}
			ch <- tui.ProgressStep{
				ID:     "build",
				Status: tui.StepComplete,
			}
			buildErrCh <- nil
		}()

		result := opts.TUI.RunProgress("auto", tui.ProgressDisplayConfig{
			Title:          "Building",
			Subtitle:       docker.DefaultImageTag,
			CompletionVerb: "Built",
		}, ch)

		if result.Err != nil {
			return result.Err
		}

		if buildErr := <-buildErrCh; buildErr != nil {
			fmt.Fprintln(ios.ErrOut)
			fmt.Fprintf(ios.ErrOut, "%s Base image build failed: %v\n", cs.FailureIcon(), buildErr)
			fmt.Fprintln(ios.ErrOut)
			fmt.Fprintln(ios.ErrOut, "Next Steps:")
			fmt.Fprintf(ios.ErrOut, "  1. %s\n", "You can manually build later with 'clawker generate latest && docker build ...'")
			fmt.Fprintf(ios.ErrOut, "  2. %s\n", "Or specify images per-project in clawker.yaml")
			return nil // early return to avoid duplicate next steps
		} else {
			// Update settings with the built image
			settings.DefaultImage = docker.DefaultImageTag
			if err := settingsLoader.Save(settings); err != nil {
				ios.Logger.Warn().Err(err).Msg("failed to update settings with default image")
				fmt.Fprintf(ios.ErrOut, "%s Warning: built image %s but failed to update settings: %v\n",
					cs.WarningIcon(), docker.DefaultImageTag, err)
			}
		}
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintf(ios.ErrOut, "  1. %s\n", "Navigate to a project directory")
	fmt.Fprintf(ios.ErrOut, "  2. %s\n", "Run 'clawker project init' to set up the project")

	return nil
}
