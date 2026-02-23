// Package init provides the project initialization subcommand.
package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// ProjectInitOptions contains the options for the project init command.
type ProjectInitOptions struct {
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	Prompter       func() *prompterpkg.Prompter
	Config         func() (config.Config, error)
	ProjectManager func() (project.ProjectManager, error)

	Name  string // Positional arg: project name
	Force bool
	Yes   bool // Non-interactive mode
}

// NewCmdProjectInit creates the project init command.
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command {
	opts := &ProjectInitOptions{
		IOStreams:      f.IOStreams,
		TUI:            f.TUI,
		Prompter:       f.Prompter,
		Config:         f.Config,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new clawker project in the current directory",
		Long: `Creates a .clawker.yaml configuration file and .clawkerignore in the current directory if they don't exist'.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

In interactive mode (default), you will be prompted to configure:
  - Project Name
  - Base container image
  - Default workspace mode (bind or snapshot)

Use --yes/-y to skip prompts and accept all defaults.`,
		Example: `  # Interactive setup (prompts for options)
  clawker project init

  # Use "my-project" as project name (still prompts for other options)
  clawker project init my-project

  # Non-interactive with all defaults
  clawker project init --yes

  # Overwrite existing configuration
  clawker project init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

// Run executes the project init command logic.
func Run(ctx context.Context, opts *ProjectInitOptions) error {
	if opts.Yes || !opts.IOStreams.IsInteractive() {
		return runNonInteractive(ctx, opts)
	}
	return runInteractive(ctx, opts)
}

// wizardContext captures external state needed by wizard field definitions.
type wizardContext struct {
	configExists   bool
	force          bool
	nameDefault    string
	configFileName string
}

// overwriteDeclined returns true when the overwrite field was answered "no".
func overwriteDeclined(vals tui.WizardValues) bool {
	return vals["overwrite"] == "no"
}

// runInteractive runs the wizard-based interactive flow.
func runInteractive(ctx context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("initializing project manager: %w", err)
	}

	configFileName := "." + cfgGateway.ProjectConfigFileName()
	configPath := filepath.Join(wd, configFileName)
	plainConfigPath := filepath.Join(wd, cfgGateway.ProjectConfigFileName())

	// Check if configuration already exists via storage layer discovery.
	configExists := shared.HasLocalProjectConfig(cfgGateway, wd)

	absPath, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	nameDefault := dirName
	if opts.Name != "" {
		nameDefault = opts.Name
	}

	// Print header
	fmt.Fprintln(ios.Out, "Setting up clawker project...")
	fmt.Fprintln(ios.Out)

	// Run wizard
	wctx := wizardContext{
		configExists:   configExists,
		force:          opts.Force,
		nameDefault:    nameDefault,
		configFileName: configFileName,
	}
	result, err := opts.TUI.RunWizard(buildProjectWizardFields(wctx))
	if err != nil {
		return fmt.Errorf("wizard failed: %w", err)
	}
	if !result.Submitted {
		fmt.Fprintln(ios.Out, "Setup cancelled.")
		return nil
	}

	// Handle overwrite-declined: register only
	if overwriteDeclined(result.Values) {
		registeredProject, regErr := projectManager.Register(ctx, dirName, wd)
		if regErr != nil {
			ios.Logger.Debug().Err(regErr).Msg("failed to register project during init (non-overwrite path)")
			return fmt.Errorf("could not register project: %w", regErr)
		}
		if registeredProject != nil {
			fmt.Fprintf(ios.Out, "%s Registered project '%s'\n", cs.SuccessIcon(), dirName)
		}

		// Still offer user-level default from existing config
		existingContent, readErr := os.ReadFile(configPath)
		if readErr != nil {
			existingContent, readErr = os.ReadFile(plainConfigPath)
			if readErr != nil {
				ios.Logger.Debug().Err(readErr).Msg("failed to read existing config for user default offer")
			}
		}
		if readErr == nil {
			prompter := opts.Prompter()
			maybeOfferUserDefault(ios, cs, prompter, existingContent)
		}
		return nil
	}

	// Extract wizard values
	projectName := result.Values["project_name"]
	buildImage := resolveImageFromWizard(result.Values)
	workspaceMode := result.Values["workspace_mode"]

	return performProjectSetup(ctx, opts, projectName, buildImage, workspaceMode)
}

// runNonInteractive runs the non-interactive (--yes) path with no prompts.
func runNonInteractive(ctx context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	configFileName := "." + cfgGateway.ProjectConfigFileName()

	// Check if configuration already exists via storage layer discovery.
	configExists := shared.HasLocalProjectConfig(cfgGateway, wd)

	if configExists && !opts.Force {
		fmt.Fprintf(ios.ErrOut, "%s %s already exists\n", cs.FailureIcon(), configFileName)
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Next Steps:")
		fmt.Fprintln(ios.ErrOut, "  - Use --force to overwrite the existing configuration")
		fmt.Fprintln(ios.ErrOut, "  - Or edit the existing .clawker.yaml manually")
		fmt.Fprintln(ios.ErrOut, "  - Or run 'clawker project register' to register the existing project")
		return fmt.Errorf("configuration already exists")
	}

	// Print header
	fmt.Fprintln(ios.ErrOut, "Setting up clawker project...")
	fmt.Fprintln(ios.ErrOut)

	absPath, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	projectName := dirName
	if opts.Name != "" {
		projectName = opts.Name
	}
	buildImage := intbuild.FlavorToImage("bookworm")
	workspaceMode := "bind"

	return performProjectSetup(ctx, opts, projectName, buildImage, workspaceMode)
}

// performProjectSetup handles file creation, registration, and success output.
// Both runInteractive and runNonInteractive delegate to this function.
func performProjectSetup(ctx context.Context, opts *ProjectInitOptions, projectName, buildImage, workspaceMode string) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("initializing project manager: %w", err)
	}

	configFileName := "." + cfgGateway.ProjectConfigFileName()
	configPath := filepath.Join(wd, configFileName)
	ignoreFileName := cfgGateway.ClawkerIgnoreName()
	ignorePath := filepath.Join(wd, ignoreFileName)

	ios.Logger.Debug().
		Str("project", projectName).
		Str("build_image", buildImage).
		Str("mode", workspaceMode).
		Str("workdir", wd).
		Bool("force", opts.Force).
		Msg("initializing project")

	// Generate config content with collected options
	configContent := scaffoldProjectConfig(buildImage, workspaceMode)

	// Create .clawker.yaml (dotfile form)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", configFileName, err)
	}
	ios.Logger.Debug().Str("file", configPath).Msg("created configuration file")

	// Create .clawkerignore
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) || opts.Force {
		if err := os.WriteFile(ignorePath, []byte(config.DefaultIgnoreFile), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", ignoreFileName, err)
		}
		ios.Logger.Debug().Str("file", ignorePath).Msg("created ignore file")
	}

	// Success output — always report files created before registration attempt
	fmt.Fprintln(ios.Out)
	fmt.Fprintf(ios.Out, "%s Created: %s\n", cs.SuccessIcon(), configFileName)
	fmt.Fprintf(ios.Out, "%s Created: %s\n", cs.SuccessIcon(), ignoreFileName)
	fmt.Fprintf(ios.Out, "%s Project: %s\n", cs.InfoIcon(), projectName)

	// Register project in user settings
	if _, err := projectManager.Register(ctx, projectName, wd); err != nil {
		return fmt.Errorf("could not register project: %w", err)
	}

	// Offer to save as user-level default if not already present
	if !opts.Yes && ios.IsInteractive() {
		prompter := opts.Prompter()
		maybeOfferUserDefault(ios, cs, prompter, []byte(configContent))
	}

	fmt.Fprintln(ios.Out)
	fmt.Fprintln(ios.Out, "Next Steps:")
	fmt.Fprintf(ios.Out, "  1. Review and customize %s\n", configFileName)
	fmt.Fprintf(ios.Out, "  2. Run 'clawker build' to build your project's container image\n")
	fmt.Fprintf(ios.Out, "  3. Run 'clawker run -it --agent <agent-name> @' to start an interactive shell in the container\n")
	return nil
}

// buildProjectWizardFields returns the wizard field definitions for interactive project init.
func buildProjectWizardFields(wctx wizardContext) []tui.WizardField {
	return []tui.WizardField{
		{
			ID:         "overwrite",
			Title:      "Overwrite",
			Prompt:     fmt.Sprintf("%s already exists. Overwrite?", wctx.configFileName),
			Kind:       tui.FieldConfirm,
			DefaultYes: false,
			SkipIf: func(_ tui.WizardValues) bool {
				return !wctx.configExists || wctx.force
			},
		},
		{
			ID:          "project_name",
			Title:       "Project",
			Prompt:      "Project name",
			Kind:        tui.FieldText,
			Default:     wctx.nameDefault,
			Placeholder: "my-project",
			Required:    true,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:         "flavor",
			Title:      "Image",
			Prompt:     "Base Linux flavor for build",
			Kind:       tui.FieldSelect,
			Options:    flavorFieldOptionsWithCustom(),
			DefaultIdx: 0,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:          "custom_image",
			Title:       "Custom Image",
			Prompt:      "Custom base image",
			Kind:        tui.FieldText,
			Placeholder: "e.g., node:20, python:3.12",
			Required:    true,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals) || vals["flavor"] != "Custom"
			},
		},
		{
			ID:     "workspace_mode",
			Title:  "Workspace",
			Prompt: "Default workspace mode",
			Kind:   tui.FieldSelect,
			Options: []tui.FieldOption{
				{Label: "bind", Description: "live sync - changes immediately affect host filesystem"},
				{Label: "snapshot", Description: "isolated copy - use git to sync changes"},
			},
			DefaultIdx: 0,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
	}
}

// flavorFieldOptionsWithCustom converts bundler flavor options to TUI wizard field options
// and appends a "Custom" option for entering a custom base image.
func flavorFieldOptionsWithCustom() []tui.FieldOption {
	flavors := intbuild.DefaultFlavorOptions()
	options := make([]tui.FieldOption, len(flavors)+1)
	for i, f := range flavors {
		options[i] = tui.FieldOption{
			Label:       f.Name,
			Description: f.Description,
		}
	}
	options[len(flavors)] = tui.FieldOption{
		Label:       "Custom",
		Description: "Enter a custom base image (e.g., node:20, python:3.12)",
	}
	return options
}

// resolveImageFromWizard converts wizard values to a Docker image reference.
func resolveImageFromWizard(values tui.WizardValues) string {
	if values["flavor"] == "Custom" {
		return values["custom_image"]
	}
	return intbuild.FlavorToImage(values["flavor"])
}

// maybeOfferUserDefault checks if a user-level clawker.yaml exists in configDir.
// If it doesn't, prompts the user to save the given content as their default.
func maybeOfferUserDefault(ios *iostreams.IOStreams, cs *iostreams.ColorScheme, prompter *prompterpkg.Prompter, content []byte) {
	userConfigPath, pathErr := config.UserProjectConfigFilePath()
	if pathErr != nil {
		return
	}
	if _, statErr := os.Stat(userConfigPath); !os.IsNotExist(statErr) {
		return // already exists or stat error
	}
	saveDefault, promptErr := prompter.Confirm(
		"Save as default project settings?",
		false,
	)
	if promptErr != nil || !saveDefault {
		return
	}
	dir := filepath.Dir(userConfigPath)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		ios.Logger.Debug().Err(mkErr).Msg("failed to create config dir for user defaults")
		return
	}
	if writeErr := os.WriteFile(userConfigPath, content, 0644); writeErr != nil {
		ios.Logger.Debug().Err(writeErr).Msg("failed to write user-level default config")
		return
	}
	fmt.Fprintf(ios.Out, "%s Default: %s\n", cs.SuccessIcon(), userConfigPath)
}

// scaffoldProjectConfig creates the .clawker.yaml content from the canonical template.
// It inserts the build image and substitutes the workspace mode into DefaultConfigYAML.
func scaffoldProjectConfig(buildImage, workspaceMode string) string {
	s := config.DefaultConfigYAML
	// Uncomment and set the image line (with fallback if template anchor changes)
	const imageAnchor = `  #image: "buildpack-deps:bookworm-scm"`
	if replaced := strings.Replace(s, imageAnchor, `  image: "`+buildImage+`"`, 1); replaced != s {
		s = replaced
	} else {
		s = strings.Replace(s, "build:\n", "build:\n  image: \""+buildImage+"\"\n", 1)
	}
	// Substitute workspace mode
	s = strings.Replace(s, `  default_mode: "bind"`, `  default_mode: "`+workspaceMode+`"`, 1)
	return s
}
