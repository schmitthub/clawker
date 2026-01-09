package config

import (
	"fmt"

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
		fmt.Printf("Error: %s not found\n\n", internalconfig.ConfigFileName)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker init' to create a configuration file")
		fmt.Println("  2. Or create claucker.yaml manually")
		return fmt.Errorf("configuration file not found")
	}

	cfg, err := loader.Load()
	if err != nil {
		fmt.Printf("Error: Failed to load configuration\n")
		fmt.Printf("  %s\n\n", err)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Check YAML syntax (indentation, colons, quotes)")
		fmt.Println("  2. Ensure all required fields are present")
		return err
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("image", cfg.Build.Image).
		Msg("configuration loaded")

	// Validate configuration
	validator := internalconfig.NewValidator(f.WorkDir)
	if err := validator.Validate(cfg); err != nil {
		fmt.Printf("Error: Configuration validation failed\n\n")

		if multiErr, ok := err.(*internalconfig.MultiValidationError); ok {
			for _, e := range multiErr.ValidationErrors() {
				fmt.Printf("  - %s\n", e)
			}
		} else {
			fmt.Printf("  %s\n", err)
		}

		fmt.Println("\nNext Steps:")
		fmt.Println("  1. Review the errors above")
		fmt.Println("  2. Edit claucker.yaml to fix the issues")
		fmt.Println("  3. Run 'claucker config check' again")
		return err
	}

	// Success output
	fmt.Println("Configuration is valid!")
	fmt.Println()
	fmt.Printf("  Project:    %s\n", cfg.Project)
	fmt.Printf("  Image:      %s\n", cfg.Build.Image)
	if cfg.Build.Dockerfile != "" {
		fmt.Printf("  Dockerfile: %s\n", cfg.Build.Dockerfile)
	}
	fmt.Printf("  Mode:       %s\n", cfg.Workspace.DefaultMode)
	fmt.Printf("  Firewall:   %t\n", cfg.Security.EnableFirewall)

	if len(cfg.Build.Packages) > 0 {
		fmt.Printf("  Packages:   %v\n", cfg.Build.Packages)
	}

	if len(cfg.Agent.Includes) > 0 {
		fmt.Printf("  Includes:   %d file(s)\n", len(cfg.Agent.Includes))
	}

	return nil
}
