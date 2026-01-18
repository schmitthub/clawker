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

// InitOptions contains the options for the init command.
type InitOptions struct {
	Force bool
}

// NewCmdInit creates the init command.
func NewCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &InitOptions{}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new Clawker project",
		Long: `Creates a clawker.yaml configuration file and .clawkerignore in the current directory.

If no project name is provided, the current directory name will be used.`,
		Example: `  # Use current directory name as project
  clawker init

  # Use "my-project" as project name
  clawker init my-project

  # Overwrite existing configuration
  clawker init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")

	return cmd
}

func runInit(f *cmdutil.Factory, opts *InitOptions, args []string) error {
	// Determine project name
	projectName := ""
	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Use current directory name
		absPath, err := filepath.Abs(f.WorkDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
		projectName = filepath.Base(absPath)
	}

	logger.Debug().
		Str("project", projectName).
		Str("workdir", f.WorkDir).
		Bool("force", opts.Force).
		Msg("initializing project")

	// Ensure user settings file exists
	settingsLoader, err := config.NewSettingsLoader()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to create settings loader")
	} else {
		created, err := settingsLoader.EnsureExists()
		if err != nil {
			logger.Debug().Err(err).Msg("failed to ensure settings file exists")
		} else if created {
			logger.Info().Str("file", settingsLoader.Path()).Msg("created user settings file")
		}
	}

	// Check if configuration already exists
	loader := config.NewLoader(f.WorkDir)
	if loader.Exists() && !opts.Force {
		cmdutil.PrintError("%s already exists", config.ConfigFileName)
		cmdutil.PrintNextSteps(
			"Use --force to overwrite the existing configuration",
			"Or edit the existing clawker.yaml manually",
		)
		return fmt.Errorf("configuration already exists")
	}

	// Create clawker.yaml
	configPath := loader.ConfigPath()
	configContent := fmt.Sprintf(config.DefaultConfigYAML, projectName)

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
	if settingsLoader != nil {
		if err := settingsLoader.AddProject(f.WorkDir); err != nil {
			logger.Debug().Err(err).Msg("failed to register project in settings")
		} else {
			logger.Info().Str("dir", f.WorkDir).Msg("registered project in user settings")
		}
	}

	// Success output
	fmt.Fprintln(os.Stderr, "Clawker project initialized!")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  Created: %s\n", config.ConfigFileName)
	fmt.Fprintf(os.Stderr, "  Created: %s\n", config.IgnoreFileName)
	fmt.Fprintf(os.Stderr, "  Project: %s\n", projectName)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next Steps:")
	fmt.Fprintln(os.Stderr, "  1. Review and customize clawker.yaml")
	fmt.Fprintln(os.Stderr, "  2. Run 'clawker start' to start Claude in a container")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Quick Reference:")
	fmt.Fprintln(os.Stderr, "  clawker start               # Start Claude (default: bind mode)")
	fmt.Fprintln(os.Stderr, "  clawker start --mode=snapshot   # Start in isolated snapshot mode")
	fmt.Fprintln(os.Stderr, "  clawker stop                # Stop the container")
	fmt.Fprintln(os.Stderr, "  clawker sh                  # Open shell in running container")

	return nil
}
