package config

import (
	"fmt"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	internalconfig "github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// NewCmdConfig creates the config command.
func NewCmdConfig(f *cmdutil2.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		Long:  `Commands for managing and validating clawker configuration.`,
	}

	cmd.AddCommand(newCmdConfigCheck(f))

	return cmd
}

func newCmdConfigCheck(f *cmdutil2.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate clawker.yaml configuration",
		Long: `Validates the clawker.yaml configuration file in the current directory.

Checks for:
  - Required fields (version, project, build.image)
  - Valid field values and formats
  - File existence for referenced paths (dockerfile, includes)
  - Security configuration consistency`,
		Example: `  # Validate configuration in current directory
  clawker config check`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigCheck(f)
		},
	}

	return cmd
}

func runConfigCheck(f *cmdutil2.Factory) error {
	ios := f.IOStreams
	logger.Debug().Str("workdir", f.WorkDir).Msg("checking configuration")

	// Load configuration
	loader := internalconfig.NewLoader(f.WorkDir)

	if !loader.Exists() {
		cmdutil2.PrintError("%s not found", internalconfig.ConfigFileName)
		cmdutil2.PrintNextSteps(
			"Run 'clawker init' to create a configuration file",
			"Or create clawker.yaml manually",
		)
		return fmt.Errorf("configuration file not found")
	}

	cfg, err := loader.Load()
	if err != nil {
		cmdutil2.PrintError("Failed to load configuration")
		fmt.Fprintf(ios.ErrOut, "  %s\n", err)
		cmdutil2.PrintNextSteps(
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
		cmdutil2.PrintError("Configuration validation failed")
		fmt.Fprintln(ios.ErrOut)

		if multiErr, ok := err.(*internalconfig.MultiValidationError); ok {
			for _, e := range multiErr.ValidationErrors() {
				fmt.Fprintf(ios.ErrOut, "  - %s\n", e)
			}
		} else {
			fmt.Fprintf(ios.ErrOut, "  %s\n", err)
		}

		cmdutil2.PrintNextSteps(
			"Review the errors above",
			"Edit clawker.yaml to fix the issues",
			"Run 'clawker config check' again",
		)
		return err
	}

	// Print any warnings
	for _, warning := range validator.Warnings() {
		cmdutil2.PrintWarning("%s", warning)
	}

	// Success output
	fmt.Fprintln(ios.ErrOut, "Configuration is valid!")
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "  Project:    %s\n", cfg.Project)
	fmt.Fprintf(ios.ErrOut, "  Image:      %s\n", cfg.Build.Image)
	if cfg.Build.Dockerfile != "" {
		fmt.Fprintf(ios.ErrOut, "  Dockerfile: %s\n", cfg.Build.Dockerfile)
	}
	fmt.Fprintf(ios.ErrOut, "  Mode:       %s\n", cfg.Workspace.DefaultMode)
	fmt.Fprintf(ios.ErrOut, "  Firewall:   %t\n", cfg.Security.FirewallEnabled())

	if len(cfg.Build.Packages) > 0 {
		fmt.Fprintf(ios.ErrOut, "  Packages:   %v\n", cfg.Build.Packages)
	}

	if len(cfg.Agent.Includes) > 0 {
		fmt.Fprintf(ios.ErrOut, "  Includes:   %d file(s)\n", len(cfg.Agent.Includes))
	}

	return nil
}
