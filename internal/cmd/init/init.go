package init

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/output"
	prompter2 "github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// InitOptions contains the options for the init command.
type InitOptions struct {
	Yes bool // Non-interactive mode
}

// NewCmdInit creates the init command for user-level setup.
func NewCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &InitOptions{}

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
			return runInit(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

func runInit(f *cmdutil.Factory, opts *InitOptions) error {
	ctx := context.Background()
	prompter := f.Prompter()

	// Print header
	fmt.Fprintln(f.IOStreams.ErrOut, "Setting up clawker user settings...")
	if !opts.Yes && f.IOStreams.IsInteractive() {
		fmt.Fprintln(f.IOStreams.ErrOut, "(Press Enter to accept defaults)")
	}
	fmt.Fprintln(f.IOStreams.ErrOut)

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

	if opts.Yes || !f.IOStreams.IsInteractive() {
		buildBaseImage = false // Default to no in non-interactive mode
	} else {
		options := []prompter2.SelectOption{
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
		flavors := cmdutil.DefaultFlavorOptions()
		selectOptions := make([]prompter2.SelectOption, len(flavors))
		for i, opt := range flavors {
			selectOptions[i] = prompter2.SelectOption{
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
		settings.Project.DefaultImage = ""
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
		fmt.Fprintln(f.IOStreams.ErrOut)
		fmt.Fprintln(f.IOStreams.ErrOut, "Starting base image build in background...")

		go func() {
			buildResultCh <- buildResult{err: cmdutil.BuildDefaultImage(ctx, selectedFlavor)}
		}()
	}

	// Save initial settings
	if err := settingsLoader.Save(settings); err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}

	logger.Info().Str("file", settingsLoader.Path()).Msg("saved user settings")

	// Success output
	fmt.Fprintln(f.IOStreams.ErrOut)
	fmt.Fprintf(f.IOStreams.ErrOut, "Created: %s\n", settingsLoader.Path())

	// Wait for build if started
	if buildBaseImage {
		fmt.Fprintln(f.IOStreams.ErrOut)
		fmt.Fprintf(f.IOStreams.ErrOut, "Building %s... (this may take a few minutes)\n", cmdutil.DefaultImageTag)

		result := <-buildResultCh

		if result.err != nil {
			fmt.Fprintln(f.IOStreams.ErrOut)
			output.PrintError("Base image build failed: %v", result.err)
			output.PrintNextSteps(
				"You can manually build later with 'clawker generate latest && docker build ...'",
				"Or specify images per-project in clawker.yaml",
			)
		} else {
			fmt.Fprintln(f.IOStreams.ErrOut)
			fmt.Fprintf(f.IOStreams.ErrOut, "Build complete! Image: %s\n", cmdutil.DefaultImageTag)

			// Update settings with the built image
			settings.Project.DefaultImage = cmdutil.DefaultImageTag
			if err := settingsLoader.Save(settings); err != nil {
				logger.Warn().Err(err).Msg("failed to update settings with default image")
			}
		}
	}

	fmt.Fprintln(f.IOStreams.ErrOut)
	output.PrintNextSteps(
		"Navigate to a project directory",
		"Run 'clawker project init' to set up the project",
	)

	return nil
}
