package check

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// CheckOptions holds options for the config check command.
type CheckOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() *config.Config
}

// NewCmdCheck creates the config check command.
func NewCmdCheck(f *cmdutil.Factory, runF func(context.Context, *CheckOptions) error) *cobra.Command {
	opts := &CheckOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate your clawker configuration from the current project's context",
		Long: `Validates the clawker configuration from this project's context'.

Checks resolution and validation between $CLAWKER_HOME/settings.yaml, $CLAWKER_HOME/clawker.yaml, and ./clawker.yaml: 
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
	io := opts.IOStreams
	cfg := opts.Config()

	settings, err := yaml.Marshal(cfg.Settings)

	if err != nil {
		return fmt.Errorf("failed to marshal project configuration: %w", err)
	}

	project, err := yaml.Marshal(cfg.Project)

	if err != nil {
		return fmt.Errorf("failed to marshal project configuration: %w", err)
	}

	fmt.Fprintln(io.Out, "Settings:")
	fmt.Fprintln(io.Out, string(settings))
	fmt.Fprintln(io.Out, "Project:")
	fmt.Fprintln(io.Out, string(project))

	return nil
}
