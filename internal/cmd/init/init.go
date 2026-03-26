// Package init provides the top-level init command, which delegates to project init.
package init

import (
	"context"
	"fmt"

	projectinit "github.com/schmitthub/clawker/internal/cmd/project/init"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdInit creates the init command, which is an alias for 'clawker project init'.
// All project initialization functionality lives in the project init command;
// this command prints an alias tip to stderr, then forwards flags and delegates.
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *projectinit.ProjectInitOptions) error) *cobra.Command {
	opts := &projectinit.ProjectInitOptions{
		IOStreams:      f.IOStreams,
		TUI:            f.TUI,
		Config:         f.Config,
		Logger:         f.Logger,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new clawker project (alias for 'project init')",
		Long: `Alias for 'clawker project init'. Initializes a new clawker project in the current
directory with language-based presets for quick setup.

See 'clawker project init --help' for full documentation.`,
		Example: `  # Interactive setup with preset picker
  clawker init

  # Specify project name
  clawker init my-project

  # Non-interactive with Bare preset defaults
  clawker init --yes

  # Non-interactive with a specific preset
  clawker init --yes --preset Go

  # Overwrite existing configuration
  clawker init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if opts.Preset != "" && !opts.Yes {
				return cmdutil.FlagErrorf("--preset requires --yes")
			}

			cs := f.IOStreams.ColorScheme()
			fmt.Fprintf(f.IOStreams.ErrOut, "%s Tip: 'clawker init' is an alias for 'clawker project init'\n\n",
				cs.InfoIcon())

			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return projectinit.Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")
	cmd.Flags().StringVar(&opts.Preset, "preset", "", "Select a language preset (requires --yes)")

	cmd.RegisterFlagCompletionFunc("preset", func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) { //nolint:errcheck // cobra registers completion internally
		return projectinit.PresetCompletions(), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
