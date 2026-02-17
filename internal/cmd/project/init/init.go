// Package init provides the project initialization subcommand.
package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	IOStreams *iostreams.IOStreams
	Prompter  func() *prompterpkg.Prompter
	Config    func() *config.Config

	Name  string // Positional arg: project name
	Force bool
	Yes   bool // Non-interactive mode
}

// NewCmdProjectInit creates the project init command.
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command {
	opts := &ProjectInitOptions{
		IOStreams: f.IOStreams,
		Prompter:  f.Prompter,
		Config:    f.Config,
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

func projectInitRun(_ context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	prompter := opts.Prompter()

	// Get current working directory (where to initialize the project)
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfgGateway := opts.Config()

	// Check if configuration already exists
	loader := config.NewProjectLoader(wd)
	if loader.Exists() && !opts.Force {
		if opts.Yes || !ios.IsInteractive() {
			cmdutil.PrintErrorf(ios, "%s already exists", config.ConfigFileName)
			cmdutil.PrintNextSteps(ios,
				"Use --force to overwrite the existing configuration",
				"Or edit the existing clawker.yaml manually",
				"Or run 'clawker project register' to register the existing project",
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
			// Don't overwrite config, but still register the project using directory name
			absPath, absErr := filepath.Abs(wd)
			if absErr != nil {
				return fmt.Errorf("resolving project path: %w", absErr)
			}
			dirName := filepath.Base(absPath)
			registryLoader := cfgGateway.Registry
			slug, err := project.RegisterProject(ios, registryLoader, wd, dirName)
			if err != nil {
				ios.Logger.Debug().Err(err).Msg("failed to register project during init (non-overwrite path)")
			}
			if slug != "" {
				fmt.Fprintf(ios.ErrOut, "%s Registered project '%s'\n", cs.SuccessIcon(), dirName)
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

	// Get default image from user settings if available (for fallback image, not build base)
	userDefaultImage := ""
	settings := cfgGateway.Settings
	if settings != nil && settings.DefaultImage != "" {
		userDefaultImage = settings.DefaultImage
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

	// Prompt for default_image (pre-built fallback image for clawker run)
	var defaultImage string
	if opts.Yes || !ios.IsInteractive() {
		// Non-interactive: use user's default_image from settings (can be empty)
		defaultImage = userDefaultImage
	} else {
		// Interactive: prompt with user's default_image as default, allow override or empty
		defaultImage, err = prompter.String(prompterpkg.PromptConfig{
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
		Str("default_image", defaultImage).
		Str("mode", workspaceMode).
		Str("workdir", wd).
		Bool("force", opts.Force).
		Msg("initializing project")

	// Generate config content with collected options
	configContent := generateConfigYAML(buildImage, defaultImage, workspaceMode)

	// Create clawker.yaml
	configPath := loader.ConfigPath()
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", config.ConfigFileName, err)
	}
	ios.Logger.Debug().Str("file", configPath).Msg("created configuration file")

	// Create .clawkerignore
	ignorePath := loader.IgnorePath()
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) || opts.Force {
		if err := os.WriteFile(ignorePath, []byte(config.DefaultIgnoreFile), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", config.IgnoreFileName, err)
		}
		ios.Logger.Debug().Str("file", ignorePath).Msg("created ignore file")
	}

	// Register project in user settings
	if _, err := project.RegisterProject(ios, cfgGateway.Registry, wd, projectName); err != nil {
		ios.Logger.Debug().Err(err).Msg("failed to register project during init")
	}

	// Success output
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), config.ConfigFileName)
	fmt.Fprintf(ios.ErrOut, "%s Created: %s\n", cs.SuccessIcon(), config.IgnoreFileName)
	fmt.Fprintf(ios.ErrOut, "%s ProjectCfg: %s\n", cs.InfoIcon(), projectName)
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
func generateConfigYAML(buildImage, defaultImage, workspaceMode string) string {
	// Only include default_image line if it's set
	defaultImageLine := ""
	if defaultImage != "" {
		defaultImageLine = fmt.Sprintf("default_image: \"%s\"\n", defaultImage)
	}

	return fmt.Sprintf(`version: "1"
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
`, defaultImageLine, buildImage, workspaceMode)
}
