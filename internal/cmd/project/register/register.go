// Package register provides the project register subcommand.
package register

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// RegisterOptions contains the options for the project register command.
type RegisterOptions struct {
	IOStreams *iostreams.IOStreams
	Prompter  func() *prompterpkg.Prompter
	Config    func() (config.Config, error)

	Name string // Positional arg: project name
	Yes  bool
}

// NewCmdProjectRegister creates the project register command.
func NewCmdProjectRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command {
	opts := &RegisterOptions{
		IOStreams: f.IOStreams,
		Prompter:  f.Prompter,
		Config:    f.Config,
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
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return projectRegisterRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, use directory name as project name")

	return cmd
}

func projectRegisterRun(ctx context.Context, opts *RegisterOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Get current working directory (where the project to register is located)
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfgGateway, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectManager := project.NewProjectManager(cfgGateway)

	// Require an existing clawker.yaml
	configFileName := "clawker.yaml"
	configPath := filepath.Join(wd, configFileName)
	if _, err := os.Stat(configPath); err != nil {
		cmdutil.PrintErrorf(ios, "No %s found in the current directory", configFileName)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker project init' to create a new project configuration",
		)
		return fmt.Errorf("no %s found", configFileName)
	}

	// Determine project name
	absPath, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	var projectName string
	if opts.Name != "" {
		projectName = opts.Name
	} else if opts.Yes || !ios.IsInteractive() {
		projectName = dirName
	} else {
		prompter := opts.Prompter()
		projectName, err = prompter.String(prompterpkg.PromptConfig{
			Message:  "ProjectCfg name",
			Default:  dirName,
			Required: true,
		})
		if err != nil {
			return fmt.Errorf("failed to get project name: %w", err)
		}
	}

	registeredProject, err := projectManager.Register(ctx, projectName, wd)
	if err != nil {
		cmdutil.PrintErrorf(ios, "Could not register project in registry: %v", err)
		return fmt.Errorf("could not register project: %w", err)
	}

	if registeredProject != nil {
		fmt.Fprintf(ios.ErrOut, "%s Registered project '%s'\n", cs.SuccessIcon(), projectName)
	}

	return nil
}
