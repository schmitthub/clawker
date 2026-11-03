package trust

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
)

var errNotInProject = errors.New(
	"not inside a registered project; run 'clawker project init' or 'clawker project register' first")

type TrustOptions struct {
	IOStreams       *iostreams.IOStreams
	Config          func() (config.Config, error)
	ProjectRegistry func() (project.Registry, error)

	Check bool
}

func NewCmdTrust(f *cmdutil.Factory, runF func(context.Context, *TrustOptions) error) *cobra.Command {
	opts := &TrustOptions{
		IOStreams:       f.IOStreams,
		Config:          f.Config,
		ProjectRegistry: f.ProjectRegistry,
		Check:           false,
	}

	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Accept the project config security settings",
		Long: `Records the current project config security settings as accepted.

The project config files live in the workspace an agent can write. Before
each build, create, run, or start, clawker compares the covered keys
(` + strings.Join(config.TrustedProjectKeyGroups(), ", ") + `) with the
settings you last accepted. A change asks you to accept it, or fails when
no prompt is possible. Review the project config, then run this command
to accept the current settings.

With --check, nothing is written: the exit status is non-zero when the
settings changed since you last accepted them.`,
		Example: `  # Accept the current project config security settings
  clawker project trust

  # Report whether the settings changed (for scripts)
  clawker project trust --check`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return trustRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Check, "check", false, "Report changes without accepting them")

	return cmd
}

func trustRun(_ context.Context, opts *TrustOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.ProjectRoot() == "" {
		return errNotInProject
	}
	reg, err := opts.ProjectRegistry()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}
	status, registered, err := shared.ProjectTrustStatus(cfg, reg)
	if err != nil {
		return fmt.Errorf("checking project config trust: %w", err)
	}
	if !registered {
		return errNotInProject
	}

	covered := strings.Join(config.TrustedProjectKeyGroups(), ", ")
	if opts.Check {
		switch {
		case status.FirstRun:
			fmt.Fprintf(ios.Out, "Project config security settings were never accepted (covered: %s)\n", covered)
			return cmdutil.SilentError
		case len(status.Changed) > 0:
			fmt.Fprintf(ios.Out, "Project config security settings changed: %s\n", strings.Join(status.Changed, ", "))
			return cmdutil.SilentError
		}
		fmt.Fprintf(ios.Out, "Project config security settings unchanged (covered: %s)\n", covered)
		return nil
	}

	if recordErr := shared.RecordProjectTrust(reg, status); recordErr != nil {
		return fmt.Errorf("accepting project config trust: %w", recordErr)
	}
	fmt.Fprintf(ios.Out, "%s Accepted the project config security settings (covered: %s)\n", cs.SuccessIcon(), covered)
	return nil
}
