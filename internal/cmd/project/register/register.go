// Package register provides the project register subcommand.
package register

import (
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/spf13/cobra"
)

// RegisterOptions contains the options for the project register command.
type RegisterOptions struct {
	IOStreams      *iostreams.IOStreams
	Prompter       func() *prompts.Prompter
	RegistryLoader func() (*config.RegistryLoader, error)
	WorkDir        string

	Yes bool
}

// NewCmdProjectRegister creates the project register command.
func NewCmdProjectRegister(f *cmdutil.Factory) *cobra.Command {
	opts := &RegisterOptions{
		IOStreams:      f.IOStreams,
		Prompter:       f.Prompter,
		RegistryLoader: f.RegistryLoader,
		WorkDir:        f.WorkDir,
	}

	cmd := &cobra.Command{
		Use:   "register [project-name]",
		Short: "Register an existing clawker project in the local registry",
		Long: `Registers the project in the current directory in the local project registry
without modifying the configuration file.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default). Use --yes to accept the directory name
without prompting.

This is useful when a clawker.yaml was manually created, copied from another
machine, or already exists and you want to register it locally.`,
		Example: `  # Register with interactive prompt for project name
  clawker project register

  # Register with a specific project name
  clawker project register my-project

  # Register using directory name without prompting
  clawker project register --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectRegister(opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, use directory name as project name")

	return cmd
}

func runProjectRegister(opts *RegisterOptions, args []string) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Require an existing clawker.yaml
	loader := config.NewLoader(opts.WorkDir)
	if !loader.Exists() {
		cmdutil.PrintError(ios, "No %s found in the current directory", config.ConfigFileName)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker project init' to create a new project configuration",
		)
		return fmt.Errorf("no %s found", config.ConfigFileName)
	}

	// Determine project name
	absPath, err := filepath.Abs(opts.WorkDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	} else if opts.Yes || !ios.IsInteractive() {
		projectName = dirName
	} else {
		prompter := opts.Prompter()
		projectName, err = prompter.String(prompts.PromptConfig{
			Message:  "Project name",
			Default:  dirName,
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get project name: %w", err)
		}
	}

	slug, err := project.RegisterProject(ios, opts.RegistryLoader, opts.WorkDir, projectName)
	if err != nil {
		return err
	}

	if slug != "" {
		fmt.Fprintf(ios.ErrOut, "%s Registered project '%s'\n", cs.SuccessIcon(), projectName)
	}

	return nil
}
