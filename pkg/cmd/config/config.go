package config

import (
	"fmt"
	"os"

	internalconfig "github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// NewCmdConfig creates the config command.
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		Long:  `Commands for managing and validating claucker configuration.`,
	}

	cmd.AddCommand(newCmdConfigCheck(f))

	return cmd
}

func newCmdConfigCheck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate claucker.yaml configuration",
		Long: `Validates the claucker.yaml configuration file in the current directory.

Checks for:
  - Required fields (version, project, build.image)
  - Valid field values and formats
  - File existence for referenced paths (dockerfile, includes)
  - Security configuration consistency`,
		Example: `  # Validate configuration in current directory
  claucker config check`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigCheck(f)
		},
	}

	return cmd
}

func runConfigCheck(f *cmdutil.Factory) error {
	logger.Debug().Str("workdir", f.WorkDir).Msg("checking configuration")

	// Load configuration
	loader := internalconfig.NewLoader(f.WorkDir)

	if !loader.Exists() {
		cmdutil.PrintError("%s not found", internalconfig.ConfigFileName)
		cmdutil.PrintNextSteps(
			"Run 'claucker init' to create a configuration file",
			"Or create claucker.yaml manually",
		)
		return fmt.Errorf("configuration file not found")
	}

	cfg, err := loader.Load()
	if err != nil {
		cmdutil.PrintError("Failed to load configuration")
		fmt.Fprintf(os.Stderr, "  %s\n", err)
		cmdutil.PrintNextSteps(
			"Check YAML syntax (indentation, colons, quotes)",
			"Ensure all required fields are present",
		)
		return err
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("image", cfg.Build.Image).
		Msg("configuration loaded")

	// Validate configuration
	validator := internalconfig.NewValidator(f.WorkDir)
	if err := validator.Validate(cfg); err != nil {
		cmdutil.PrintError("Configuration validation failed")
		fmt.Fprintln(os.Stderr)

		if multiErr, ok := err.(*internalconfig.MultiValidationError); ok {
			for _, e := range multiErr.ValidationErrors() {
				fmt.Fprintf(os.Stderr, "  - %s\n", e)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  %s\n", err)
		}

		cmdutil.PrintNextSteps(
			"Review the errors above",
			"Edit claucker.yaml to fix the issues",
			"Run 'claucker config check' again",
		)
		return err
	}

	// Success output
	fmt.Fprintln(os.Stderr, "Configuration is valid!")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  Project:    %s\n", cfg.Project)
	fmt.Fprintf(os.Stderr, "  Image:      %s\n", cfg.Build.Image)
	if cfg.Build.Dockerfile != "" {
		fmt.Fprintf(os.Stderr, "  Dockerfile: %s\n", cfg.Build.Dockerfile)
	}
	fmt.Fprintf(os.Stderr, "  Mode:       %s\n", cfg.Workspace.DefaultMode)
	fmt.Fprintf(os.Stderr, "  Firewall:   %t\n", cfg.Security.EnableFirewall)

	if len(cfg.Build.Packages) > 0 {
		fmt.Fprintf(os.Stderr, "  Packages:   %v\n", cfg.Build.Packages)
	}

	if len(cfg.Agent.Includes) > 0 {
		fmt.Fprintf(os.Stderr, "  Includes:   %d file(s)\n", len(cfg.Agent.Includes))
	}

	return nil
}
