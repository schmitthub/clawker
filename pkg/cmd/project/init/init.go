// Package init provides the project initialization subcommand.
package init

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
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
	prompter := f.Prompter()

	// Print header
	fmt.Fprintln(f.IOStreams.ErrOut, "Setting up clawker project...")
	if !opts.Yes && f.IOStreams.IsInteractive() {
		fmt.Fprintln(f.IOStreams.ErrOut, "(Press Enter to accept defaults)")
	}
	fmt.Fprintln(f.IOStreams.ErrOut)

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
	} else if opts.Yes || !f.IOStreams.IsInteractive() {
		// Non-interactive: use directory name
		projectName = dirName
	} else {
		// Interactive: prompt for project name
		projectName, err = prompter.String(cmdutil.PromptConfig{
			Message:  "Project name",
			Default:  dirName,
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get project name: %w", err)
		}
	}

	// Get default image from user settings if available
	defaultImage := ""
	settings, err := f.Settings()
	if err == nil && settings != nil && settings.Project.DefaultImage != "" {
		defaultImage = settings.Project.DefaultImage
	}

	// Prompt for base image
	var baseImage string
	if opts.Yes || !f.IOStreams.IsInteractive() {
		if defaultImage == "" {
			cmdutil.PrintError("No default image configured")
			cmdutil.PrintNextSteps(
				"Run 'clawker init' first to set up user defaults and build a base image",
				"Or run interactively without --yes to specify an image",
			)
			return fmt.Errorf("no default image configured; run 'clawker init' first or specify interactively")
		}
		baseImage = defaultImage
	} else {
		// In interactive mode, prompt with default if available, otherwise require input
		baseImage, err = prompter.String(cmdutil.PromptConfig{
			Message:  "Base image",
			Default:  defaultImage, // Empty string means no default shown
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get base image: %w", err)
		}
	}

	// Prompt for workspace mode
	var workspaceMode string
	if opts.Yes || !f.IOStreams.IsInteractive() {
		workspaceMode = "bind"
	} else {
		options := []cmdutil.SelectOption{
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
		Str("image", baseImage).
		Str("mode", workspaceMode).
		Str("workdir", f.WorkDir).
		Bool("force", opts.Force).
		Msg("initializing project")

	// Check if configuration already exists
	loader := config.NewLoader(f.WorkDir)
	if loader.Exists() && !opts.Force {
		if opts.Yes || !f.IOStreams.IsInteractive() {
			cmdutil.PrintError("%s already exists", config.ConfigFileName)
			cmdutil.PrintNextSteps(
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
			fmt.Fprintln(f.IOStreams.ErrOut, "Aborted.")
			return nil
		}
	}

	// Generate config content with collected options
	configContent := generateConfigYAML(projectName, baseImage, workspaceMode)

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
		fmt.Fprintf(f.IOStreams.ErrOut, "Note: Could not access user settings: %v\n", err)
	} else {
		// Ensure settings file exists
		_, err := settingsLoader.EnsureExists()
		if err != nil {
			logger.Debug().Err(err).Msg("failed to ensure settings file exists")
		}
		// Register the project
		if err := settingsLoader.AddProject(f.WorkDir); err != nil {
			logger.Debug().Err(err).Msg("failed to register project in settings")
			fmt.Fprintf(f.IOStreams.ErrOut, "Note: Could not register project in settings: %v\n", err)
		} else {
			logger.Info().Str("dir", f.WorkDir).Msg("registered project in user settings")
		}
	}

	// Success output
	fmt.Fprintln(f.IOStreams.ErrOut)
	fmt.Fprintf(f.IOStreams.ErrOut, "Created: %s\n", config.ConfigFileName)
	fmt.Fprintf(f.IOStreams.ErrOut, "Created: %s\n", config.IgnoreFileName)
	fmt.Fprintf(f.IOStreams.ErrOut, "Project: %s\n", projectName)
	fmt.Fprintln(f.IOStreams.ErrOut)
	cmdutil.PrintNextSteps(
		"Review and customize clawker.yaml",
		"Run 'clawker start' to start Claude in a container",
	)

	return nil
}

// generateConfigYAML creates the clawker.yaml content with the given options.
func generateConfigYAML(projectName, baseImage, workspaceMode string) string {
	return fmt.Sprintf(`version: "1"
project: "%s"

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
`, projectName, baseImage, workspaceMode)
}
