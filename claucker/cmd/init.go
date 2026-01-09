package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/claucker/claucker/internal/config"
	"github.com/claucker/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	initForce bool
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init [project-name]",
	Short: "Initialize a new Claucker project",
	Long: `Creates a claucker.yaml configuration file and .clauckerignore in the current directory.

If no project name is provided, the current directory name will be used.

Examples:
  claucker init                  # Use current directory name as project
  claucker init my-project       # Use "my-project" as project name
  claucker init --force          # Overwrite existing configuration`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Overwrite existing configuration files")
}

func runInit(cmd *cobra.Command, args []string) error {
	// Determine project name
	projectName := ""
	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Use current directory name
		absPath, err := filepath.Abs(workDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
		projectName = filepath.Base(absPath)
	}

	logger.Debug().
		Str("project", projectName).
		Str("workdir", workDir).
		Bool("force", initForce).
		Msg("initializing project")

	// Check if configuration already exists
	loader := config.NewLoader(workDir)
	if loader.Exists() && !initForce {
		fmt.Printf("Error: %s already exists\n\n", config.ConfigFileName)
		fmt.Println("Next Steps:")
		fmt.Println("  1. Use --force to overwrite the existing configuration")
		fmt.Println("  2. Or edit the existing claucker.yaml manually")
		return fmt.Errorf("configuration already exists")
	}

	// Create claucker.yaml
	configPath := loader.ConfigPath()
	configContent := fmt.Sprintf(config.DefaultConfigYAML, projectName)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", config.ConfigFileName, err)
	}

	logger.Info().Str("file", configPath).Msg("created configuration file")

	// Create .clauckerignore
	ignorePath := loader.IgnorePath()
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) || initForce {
		if err := os.WriteFile(ignorePath, []byte(config.DefaultIgnoreFile), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", config.IgnoreFileName, err)
		}
		logger.Info().Str("file", ignorePath).Msg("created ignore file")
	}

	// Success output
	fmt.Println("Claucker project initialized!")
	fmt.Println()
	fmt.Printf("  Created: %s\n", config.ConfigFileName)
	fmt.Printf("  Created: %s\n", config.IgnoreFileName)
	fmt.Printf("  Project: %s\n", projectName)
	fmt.Println()
	fmt.Println("Next Steps:")
	fmt.Println("  1. Review and customize claucker.yaml")
	fmt.Println("  2. Run 'claucker up' to start Claude in a container")
	fmt.Println()
	fmt.Println("Quick Reference:")
	fmt.Println("  claucker up              # Start Claude (default: bind mode)")
	fmt.Println("  claucker up --mode=snapshot  # Start in isolated snapshot mode")
	fmt.Println("  claucker down            # Stop the container")
	fmt.Println("  claucker sh              # Open shell in running container")

	return nil
}
