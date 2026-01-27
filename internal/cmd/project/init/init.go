// Package init provides the project initialization subcommand.
package init

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/spf13/cobra"
)

// ProjectInitOptions contains the options for the project init command.
type ProjectInitOptions struct {
	Force bool
	Yes   bool // Non-interactive mode
}

// NewCmdProjectInit creates the project init command.
func NewCmdProjectInit(f *cmdutil.Factory) *cobra.Command {
	opts := &ProjectInitOptions{}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new clawker project in the current directory",
		Long: `Creates a clawker.yaml configuration file and .clawkerignore in the current directory.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

In interactive mode (default), you will be prompted to configure:
  - Project name
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
			return runProjectInit(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

func runProjectInit(f *cmdutil.Factory, opts *ProjectInitOptions, args []string) error {
	ios := f.IOStreams
	cs := ios.ColorScheme()
	prompter := f.Prompter()

	// Print header
	fmt.Fprintln(ios.ErrOut, "Setting up clawker project...")
	if !opts.Yes && ios.IsInteractive() {
		fmt.Fprintln(ios.ErrOut, "(Press Enter to accept defaults)")
	}
	fmt.Fprintln(ios.ErrOut)

	// Get absolute path of working directory
	absPath, err := filepath.Abs(f.WorkDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	// Determine project name
	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	} else if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use directory name
		projectName = dirName
	} else {
		// Interactive: prompt for project name
		projectName, err = prompter.String(prompts.PromptConfig{
			Message:  "Project name",
			Default:  dirName,
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get project name: %w", err)
		}
	}

	// Get default image from user settings if available (for fallback image, not build base)
	userDefaultImage := ""
	settings, err := f.Settings()
	if err == nil && settings != nil && settings.Project.DefaultImage != "" {
		userDefaultImage = settings.Project.DefaultImage
	}

	// Prompt for build.image (base Linux flavor for Dockerfile FROM)
	var buildImage string
	if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use buildpack-deps:bookworm-scm as default
		buildImage = cmdutil.FlavorToImage("bookworm")
	} else {
		// Interactive: show flavor options + Custom option
		flavors := cmdutil.DefaultFlavorOptions()
		selectOptions := make([]prompts.SelectOption, len(flavors)+1)
		for i, opt := range flavors {
			selectOptions[i] = prompts.SelectOption{
				Label:       opt.Name,
				Description: opt.Description,
			}
		}
		selectOptions[len(flavors)] = prompts.SelectOption{
			Label:       "Custom",
			Description: "Enter a custom base image (e.g., node:20, python:3.12)",
		}

		idx, err := prompter.Select("Base Linux flavor for build", selectOptions, 0)
		if err != nil {
			return fmt.Errorf("failed to get base flavor: %w", err)
		}

		if idx == len(flavors) {
			// Custom option selected - prompt for custom image
			customImage, err := prompter.String(prompts.PromptConfig{
				Message:  "Custom base image",
				Required: true,
			})
			if err != nil {
				return fmt.Errorf("failed to get custom base image: %w", err)
			}
			buildImage = customImage
		} else {
			// Map flavor name to full image reference
			buildImage = cmdutil.FlavorToImage(selectOptions[idx].Label)
		}
	}

	// Prompt for default_image (pre-built fallback image for clawker run)
	var defaultImage string
	if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use user's default_image from settings (can be empty)
		defaultImage = userDefaultImage
	} else {
		// Interactive: prompt with user's default_image as default, allow override or empty
		defaultImage, err = prompter.String(prompts.PromptConfig{
			Message:  "Default fallback image (leave empty if none)",
			Default:  userDefaultImage,
			Required: false,
		})
		if err != nil {
			return fmt.Errorf("failed to get default image: %w", err)
		}
	}

	// Prompt for workspace mode
	var workspaceMode string
	if opts.Yes || !ios.IsInteractive() {
		workspaceMode = "bind"
	} else {
		options := []prompts.SelectOption{
			{Label: "bind", Description: "live sync - changes immediately affect host filesystem"},
			{Label: "snapshot", Description: "isolated copy - use git to sync changes"},
		}
		idx, err := prompter.Select("Default workspace mode", options, 0)
		if err != nil {
			return fmt.Errorf("failed to get workspace mode: %w", err)
		}
		workspaceMode = options[idx].Label
	}

	logger.Debug().
		Str("project", projectName).
		Str("build_image", buildImage).
		Str("default_image", defaultImage).
		Str("mode", workspaceMode).
		Str("workdir", f.WorkDir).
		Bool("force", opts.Force).
		Msg("initializing project")

	// Check if configuration already exists
	loader := config.NewLoader(f.WorkDir)
	if loader.Exists() && !opts.Force {
		if opts.Yes || !ios.IsInteractive() {
			cmdutil.PrintError(ios, "%s already exists", config.ConfigFileName)
			cmdutil.PrintNextSteps(ios,
				"Use --force to overwrite the existing configuration",
				"Or edit the existing clawker.yaml manually",
			)
			return fmt.Errorf("configuration already exists")
		}
		// Interactive: ask for confirmation
		overwrite, err := prompter.Confirm(
			fmt.Sprintf("%s already exists. Overwrite?", config.ConfigFileName),
			false,
		)
		if err != nil {
			return fmt.Errorf("failed to get confirmation: %w", err)
		}
		if !overwrite {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	// Generate config content with collected options
	configContent := generateConfigYAML(projectName, buildImage, defaultImage, workspaceMode)

	// Create clawker.yaml
	configPath := loader.ConfigPath()
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", config.ConfigFileName, err)
	}
	logger.Info().Str("file", configPath).Msg("created configuration file")

	// Create .clawkerignore
	ignorePath := loader.IgnorePath()
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) || opts.Force {
		if err := os.WriteFile(ignorePath, []byte(config.DefaultIgnoreFile), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", config.IgnoreFileName, err)
		}
		logger.Info().Str("file", ignorePath).Msg("created ignore file")
	}

	// Register project in user settings
	settingsLoader, err := config.NewSettingsLoader()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to create settings loader")
		fmt.Fprintf(ios.ErrOut, "%s Could not access user settings: %v\n", cs.WarningIcon(), err)
	} else {
		// Ensure settings file exists
		_, err := settingsLoader.EnsureExists()
		if err != nil {
			logger.Debug().Err(err).Msg("failed to ensure settings file exists")
		}
		// Register the project
		if err := settingsLoader.AddProject(f.WorkDir); err != nil {
			logger.Debug().Err(err).Msg("failed to register project in settings")
			fmt.Fprintf(ios.ErrOut, "%s Could not register project in settings: %v\n", cs.WarningIcon(), err)
		} else {
			logger.Info().Str("dir", f.WorkDir).Msg("registered project in user settings")
		}
	}

	// Success output
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), config.ConfigFileName)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), config.IgnoreFileName)
	fmt.Fprintf(ios.ErrOut, "%s Project: %s\n", cs.InfoIcon(), projectName)
	fmt.Fprintln(ios.ErrOut)
	cmdutil.PrintNextSteps(ios,
		"Review and customize clawker.yaml",
		"Run 'clawker start' to start Claude in a container",
	)

	return nil
}

// generateConfigYAML creates the clawker.yaml content with the given options.
// buildImage is the base Linux flavor for Dockerfile FROM (e.g., buildpack-deps:bookworm-scm).
// defaultImage is the pre-built fallback image for clawker run when no project image exists.
func generateConfigYAML(projectName, buildImage, defaultImage, workspaceMode string) string {
	// Only include default_image line if it's set
	defaultImageLine := ""
	if defaultImage != "" {
		defaultImageLine = fmt.Sprintf("default_image: \"%s\"\n", defaultImage)
	}

	return fmt.Sprintf(`version: "1"
project: "%s"
%s
build:
  image: "%s"
  packages:
    - git
    - curl
    - ripgrep
  instructions:
    env: {}
    # copy: []
    # root_run: []
    # user_run: []
  # inject:
  #   after_from: []
  #   after_packages: []

agent:
  includes: []
  env: {}
  # shell: "/bin/bash"
  # editor: "vim"
  # visual: "vim"

workspace:
  remote_path: "/workspace"
  default_mode: "%s"

security:
  enable_firewall: true
  docker_socket: false
  # enable_host_proxy: true
  # git_credentials:
  #   forward_https: true
  #   forward_ssh: true
  #   copy_git_config: true
  # allowed_domains: []
  # cap_add: []
`, projectName, defaultImageLine, buildImage, workspaceMode)
}
