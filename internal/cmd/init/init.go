package init

import (
	"context"
	"fmt"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// InitOptions contains the options for the init command.
type InitOptions struct {
	IOStreams *iostreams.IOStreams
	Prompter  func() *prompterpkg.Prompter

	Yes bool // Non-interactive mode
}

// NewCmdInit creates the init command for user-level setup.
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command {
	opts := &InitOptions{
		IOStreams: f.IOStreams,
		Prompter:  f.Prompter,
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
			return initRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

func initRun(ctx context.Context, opts *InitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	prompter := opts.Prompter()

	// Print header
	fmt.Fprintln(ios.ErrOut, "Setting up clawker user settings...")
	if !opts.Yes && ios.IsInteractive() {
		fmt.Fprintln(ios.ErrOut, "(Press Enter to accept defaults)")
	}
	fmt.Fprintln(ios.ErrOut)

	// Ensure settings loader is available
	settingsLoader, err := config.NewSettingsLoader()
	if err != nil {
		return fmt.Errorf("failed to create settings loader: %w", err)
	}

	// Load existing settings or create defaults
	settings, err := settingsLoader.Load()
	if err != nil {
		settings = config.DefaultSettings()
	}

	// Ask if user wants to build base image
	var buildBaseImage bool
	var selectedFlavor string

	if opts.Yes || !ios.IsInteractive() {
		buildBaseImage = false // Default to no in non-interactive mode
	} else {
		options := []prompterpkg.SelectOption{
			{Label: "Yes", Description: "Build a clawker-optimized base image (Recommended)"},
			{Label: "No", Description: "Skip - specify images per-project later"},
		}
		idx, err := prompter.Select("Build an initial base image?", options, 0)
		if err != nil {
			return fmt.Errorf("failed to get build preference: %w", err)
		}
		buildBaseImage = (idx == 0)
	}

	if buildBaseImage {
		// Convert flavor options to SelectOption
		flavors := intbuild.DefaultFlavorOptions()
		selectOptions := make([]prompterpkg.SelectOption, len(flavors))
		for i, opt := range flavors {
			selectOptions[i] = prompterpkg.SelectOption{
				Label:       opt.Name,
				Description: opt.Description,
			}
		}

		idx, err := prompter.Select("Select Linux flavor", selectOptions, 0)
		if err != nil {
			return fmt.Errorf("failed to get flavor selection: %w", err)
		}
		selectedFlavor = flavors[idx].Name

		// Clear default image when building (will be set after successful build)
		settings.DefaultImage = ""
	}

	logger.Debug().
		Bool("build_base_image", buildBaseImage).
		Str("flavor", selectedFlavor).
		Msg("initializing user settings")

	// Start build in background if requested
	type buildResult struct {
		err error
	}
	buildResultCh := make(chan buildResult, 1)

	if buildBaseImage {
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintf(ios.ErrOut, "%s Starting base image build...\n", cs.InfoIcon())

		go func() {
			buildResultCh <- buildResult{err: docker.BuildDefaultImage(ctx, selectedFlavor)}
		}()
	}

	// Save initial settings
	if err := settingsLoader.Save(settings); err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}

	logger.Info().Str("file", settingsLoader.Path()).Msg("saved user settings")

	// Success output
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), settingsLoader.Path())

	// Wait for build if started
	if buildBaseImage {
		fmt.Fprintln(ios.ErrOut)
		ios.StartProgressIndicatorWithLabel(fmt.Sprintf("Building %s...", docker.DefaultImageTag))

		result := <-buildResultCh

		ios.StopProgressIndicator()

		if result.err != nil {
			fmt.Fprintln(ios.ErrOut)
			cmdutil.PrintError(ios, "Base image build failed: %v", result.err)
			cmdutil.PrintNextSteps(ios,
				"You can manually build later with 'clawker generate latest && docker build ...'",
				"Or specify images per-project in clawker.yaml",
			)
		} else {
			fmt.Fprintln(ios.ErrOut)
			fmt.Fprintf(ios.ErrOut, "%s Build complete! Image: %s\n", cs.SuccessIcon(), docker.DefaultImageTag)

			// Update settings with the built image
			settings.DefaultImage = docker.DefaultImageTag
			if err := settingsLoader.Save(settings); err != nil {
				logger.Warn().Err(err).Msg("failed to update settings with default image")
			}
		}
	}

	fmt.Fprintln(ios.ErrOut)
	cmdutil.PrintNextSteps(ios,
		"Navigate to a project directory",
		"Run 'clawker project init' to set up the project",
	)

	return nil
}
