// Package init provides the project initialization subcommand.
package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// ProjectInitOptions contains the options for the project init command.
type ProjectInitOptions struct {
	IOStreams      *iostreams.IOStreams
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
		Prompter:       f.Prompter,
		Config:         f.Config,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new clawker project in the current directory",
		Long: `Creates a clawker.yaml configuration file and .clawkerignore in the current directory if they don't exist'.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

In interactive mode (default), you will be prompted to configure:
  - ProjectCfg name
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
			return projectInitRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

func projectInitRun(ctx context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	prompter := opts.Prompter()

	// Get current working directory (where to initialize the project)
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
	configFileName := "." + cfgGateway.ProjectConfigFileName() // dotfile form for local project config
	configPath := filepath.Join(wd, configFileName)
	ignoreFileName := cfgGateway.ClawkerIgnoreName()
	ignorePath := filepath.Join(wd, ignoreFileName)

	// Check if configuration already exists (either dotfile or plain form)
	plainConfigPath := filepath.Join(wd, cfgGateway.ProjectConfigFileName())
	configExists := false
	if _, err := os.Stat(configPath); err == nil {
		configExists = true
	} else if _, err := os.Stat(plainConfigPath); err == nil {
		configExists = true
	}
	if configExists && !opts.Force {
		if opts.Yes || !ios.IsInteractive() {
			fmt.Fprintf(ios.ErrOut, "%s %s already exists\n", cs.FailureIcon(), configFileName)
			fmt.Fprintln(ios.ErrOut)
			fmt.Fprintln(ios.ErrOut, "Next Steps:")
			fmt.Fprintln(ios.ErrOut, "  - Use --force to overwrite the existing configuration")
			fmt.Fprintln(ios.ErrOut, "  - Or edit the existing clawker.yaml manually")
			fmt.Fprintln(ios.ErrOut, "  - Or run 'clawker project register' to register the existing project")
			return fmt.Errorf("configuration already exists")
		}
		// Interactive: ask for confirmation
		overwrite, err := prompter.Confirm(
			fmt.Sprintf("%s already exists. Overwrite?", configFileName),
			false,
		)
		if err != nil {
			return fmt.Errorf("failed to get confirmation: %w", err)
		}
		if !overwrite {
			// Don't overwrite config, but still register the project using directory name
			absPath, absErr := filepath.Abs(wd)
			if absErr != nil {
				return fmt.Errorf("resolving project path: %w", absErr)
			}
			dirName := filepath.Base(absPath)
			registeredProject, err := projectManager.Register(ctx, dirName, wd)
			if err != nil {
				ios.Logger.Debug().Err(err).Msg("failed to register project during init (non-overwrite path)")
			}
			if registeredProject != nil {
				fmt.Fprintf(ios.ErrOut, "%s Registered project '%s'\n", cs.SuccessIcon(), dirName)
			}

			// Still offer user-level default even when not overwriting local config
			existingContent, readErr := os.ReadFile(configPath)
			if readErr != nil {
				// Try plain form if dotfile not found
				existingContent, readErr = os.ReadFile(plainConfigPath)
			}
			if readErr == nil {
				maybeOfferUserDefault(ios, cs, prompter, existingContent)
			}
			return nil
		}
	}

	// Print header
	fmt.Fprintln(ios.ErrOut, "Setting up clawker project...")
	if !opts.Yes && ios.IsInteractive() {
		fmt.Fprintln(ios.ErrOut, "(Press Enter to accept defaults)")
	}
	fmt.Fprintln(ios.ErrOut)

	// Get absolute path of working directory
	absPath, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	// Determine project name
	var projectName string
	if opts.Name != "" {
		projectName = opts.Name
	} else if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use directory name
		projectName = dirName
	} else {
		// Interactive: prompt for project name
		projectName, err = prompter.String(prompterpkg.PromptConfig{
			Message:  "ProjectCfg name",
			Default:  dirName,
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get project name: %w", err)
		}
	}

	// Prompt for build.image (base Linux flavor for Dockerfile FROM)
	var buildImage string
	if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use buildpack-deps:bookworm-scm as default
		buildImage = intbuild.FlavorToImage("bookworm")
	} else {
		// Interactive: show flavor options + Custom option
		flavors := intbuild.DefaultFlavorOptions()
		selectOptions := make([]prompterpkg.SelectOption, len(flavors)+1)
		for i, opt := range flavors {
			selectOptions[i] = prompterpkg.SelectOption{
				Label:       opt.Name,
				Description: opt.Description,
			}
		}
		selectOptions[len(flavors)] = prompterpkg.SelectOption{
			Label:       "Custom",
			Description: "Enter a custom base image (e.g., node:20, python:3.12)",
		}

		idx, err := prompter.Select("Base Linux flavor for build", selectOptions, 0)
		if err != nil {
			return fmt.Errorf("failed to get base flavor: %w", err)
		}

		if idx == len(flavors) {
			// Custom option selected - prompt for custom image
			customImage, err := prompter.String(prompterpkg.PromptConfig{
				Message:  "Custom base image",
				Required: true,
			})
			if err != nil {
				return fmt.Errorf("failed to get custom base image: %w", err)
			}
			buildImage = customImage
		} else {
			// Map flavor name to full image reference
			buildImage = intbuild.FlavorToImage(selectOptions[idx].Label)
		}
	}

	// Prompt for workspace mode
	var workspaceMode string
	if opts.Yes || !ios.IsInteractive() {
		workspaceMode = "bind"
	} else {
		options := []prompterpkg.SelectOption{
			{Label: "bind", Description: "live sync - changes immediately affect host filesystem"},
			{Label: "snapshot", Description: "isolated copy - use git to sync changes"},
		}
		idx, err := prompter.Select("Default workspace mode", options, 0)
		if err != nil {
			return fmt.Errorf("failed to get workspace mode: %w", err)
		}
		workspaceMode = options[idx].Label
	}

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

	// Register project in user settings
	if _, err := projectManager.Register(ctx, projectName, wd); err != nil {
		ios.Logger.Debug().Err(err).Msg("failed to register project during init")
	}

	// Success output
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), configFileName)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), ignoreFileName)
	fmt.Fprintf(ios.ErrOut, "%s Project: %s\n", cs.InfoIcon(), projectName)

	// Offer to save as user-level default if not already present
	if !opts.Yes && ios.IsInteractive() {
		maybeOfferUserDefault(ios, cs, prompter, []byte(configContent))
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintf(ios.ErrOut, "  1. Review and customize %s\n", configFileName)
	fmt.Fprintf(ios.ErrOut, "  2. Run 'clawker start' to start Claude in a container\n")

	return nil
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
	fmt.Fprintf(ios.ErrOut, "%s Default: %s\n", cs.SuccessIcon(), userConfigPath)
}

// scaffoldProjectConfig creates the clawker.yaml content from the canonical template.
// It inserts the build image and substitutes the workspace mode into DefaultConfigYAML.
func scaffoldProjectConfig(buildImage, workspaceMode string) string {
	s := config.DefaultConfigYAML
	// Insert image line before the first comment in the build section
	s = strings.Replace(s, "  # Base image for the container", "  image: \""+buildImage+"\"\n  # Base image for the container", 1)
	// Substitute workspace mode
	s = strings.Replace(s, `  default_mode: "bind"`, `  default_mode: "`+workspaceMode+`"`, 1)
	return s
}
