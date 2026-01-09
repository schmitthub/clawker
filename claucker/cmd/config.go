package cmd

import (
	"fmt"

	"github.com/claucker/claucker/internal/config"
	"github.com/claucker/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management commands",
	Long:  `Commands for managing and validating claucker configuration.`,
}

// configCheckCmd represents the config check command
var configCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate claucker.yaml configuration",
	Long: `Validates the claucker.yaml configuration file in the current directory.

Checks for:
  - Required fields (version, project, build.image)
  - Valid field values and formats
  - File existence for referenced paths (dockerfile, includes)
  - Security configuration consistency`,
	RunE: runConfigCheck,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configCheckCmd)
}

func runConfigCheck(cmd *cobra.Command, args []string) error {
	logger.Debug().Str("workdir", workDir).Msg("checking configuration")

	// Load configuration
	loader := config.NewLoader(workDir)

	if !loader.Exists() {
		fmt.Printf("Error: %s not found\n\n", config.ConfigFileName)
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
	validator := config.NewValidator(workDir)
	if err := validator.Validate(cfg); err != nil {
		fmt.Printf("Error: Configuration validation failed\n\n")

		if multiErr, ok := err.(*config.MultiValidationError); ok {
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
