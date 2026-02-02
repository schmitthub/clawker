package check

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	internalconfig "github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// CheckOptions holds options for the config check command.
type CheckOptions struct {
	IOStreams *iostreams.IOStreams
	WorkDir func() (string, error)
}

// NewCmdCheck creates the config check command.
func NewCmdCheck(f *cmdutil.Factory, runF func(context.Context, *CheckOptions) error) *cobra.Command {
	opts := &CheckOptions{
		IOStreams: f.IOStreams,
		WorkDir: f.WorkDir,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return checkRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func checkRun(_ context.Context, opts *CheckOptions) error {
	ios := opts.IOStreams

	wd, err := opts.WorkDir()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	logger.Debug().Str("workdir", wd).Msg("checking configuration")

	// Load configuration
	loader := internalconfig.NewLoader(wd)

	if !loader.Exists() {
		cmdutil.PrintError(ios, "%s not found", internalconfig.ConfigFileName)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to create a configuration file",
			"Or create clawker.yaml manually",
		)
		return fmt.Errorf("configuration file not found")
	}

	cfg, err := loader.Load()
	if err != nil {
		cmdutil.PrintError(ios, "Failed to load configuration")
		fmt.Fprintf(ios.ErrOut, "  %s\n", err)
		cmdutil.PrintNextSteps(ios,
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
	validator := internalconfig.NewValidator(wd)
	if err := validator.Validate(cfg); err != nil {
		cmdutil.PrintError(ios, "Configuration validation failed")
		fmt.Fprintln(ios.ErrOut)

		if multiErr, ok := err.(*internalconfig.MultiValidationError); ok {
			for _, e := range multiErr.ValidationErrors() {
				fmt.Fprintf(ios.ErrOut, "  - %s\n", e)
			}
		} else {
			fmt.Fprintf(ios.ErrOut, "  %s\n", err)
		}

		cmdutil.PrintNextSteps(ios,
			"Review the errors above",
			"Edit clawker.yaml to fix the issues",
			"Run 'clawker config check' again",
		)
		return err
	}

	// Print any warnings
	for _, warning := range validator.Warnings() {
		cmdutil.PrintWarning(ios, "%s", warning)
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
