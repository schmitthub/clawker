// Package validate provides the `clawker bundle validate` command.
package validate

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
)

// ValidateOptions holds the options for the bundle validate command.
type ValidateOptions struct {
	IOStreams     *iostreams.IOStreams
	BundleManager func() (*bundle.Manager, error)

	Dir    string
	Strict bool
}

// NewCmdValidate creates the bundle validate command.
func NewCmdValidate(f *cmdutil.Factory, runF func(context.Context, *ValidateOptions) error) *cobra.Command {
	opts := &ValidateOptions{
		IOStreams:     f.IOStreams,
		BundleManager: f.BundleManager,
		Dir:           "",
		Strict:        false,
	}

	cmd := &cobra.Command{
		Use:   "validate <dir>",
		Short: "Validate a bundle directory",
		Long: `Validates a bundle directory before publishing: the .clawker-bundle/bundle.yaml
manifest must be present and well-formed with the required namespace and name,
its component convention directories are checked, and every component is
loaded through the same front door the consuming commands use — a harness,
stack, or monitoring manifest that would break at build or monitor time fails
here instead.

A malformed or missing manifest, a missing required field, a reserved
namespace, or an invalid component is a hard failure. Unknown top-level
directories (with typo suggestions) and empty convention directories are
advisory warnings; --strict turns every warning into a failure. Validation is
local — it never fetches.`,
		Example: `  # Validate a bundle directory
  clawker bundle validate ./my-bundle

  # Treat warnings as failures (for CI / authors)
  clawker bundle validate ./my-bundle --strict`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Dir = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return validateRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Strict, "strict", false, "Treat advisory warnings as validation failures")

	return cmd
}

func validateRun(_ context.Context, opts *ValidateOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}

	report := mgr.Validate(opts.Dir)

	if report.LoadErr != nil {
		fmt.Fprintf(ios.ErrOut, "%s %v\n", cs.FailureIcon(), report.LoadErr)
		return cmdutil.SilentError
	}

	for _, w := range report.Warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w.Message)
	}

	componentFailures := 0
	for _, c := range report.Bundle.Components {
		if compErr := loadComponent(c); compErr != nil {
			fmt.Fprintf(ios.ErrOut, "%s %v\n", cs.FailureIcon(), compErr)
			componentFailures++
		}
	}
	if componentFailures > 0 {
		fmt.Fprintf(ios.ErrOut, "%s bundle %s has %d invalid component(s)\n",
			cs.FailureIcon(), opts.Dir, componentFailures)
		return cmdutil.SilentError
	}

	if !report.OK(opts.Strict) {
		fmt.Fprintf(ios.ErrOut, "%s bundle %s failed strict validation with %d warning(s)\n",
			cs.FailureIcon(), opts.Dir, len(report.Warnings))
		return cmdutil.SilentError
	}

	if len(report.Warnings) > 0 {
		fmt.Fprintf(ios.Out, "%s bundle %s is valid (%d warning(s))\n",
			cs.SuccessIcon(), opts.Dir, len(report.Warnings))
	} else {
		fmt.Fprintf(ios.Out, "%s bundle %s is valid\n", cs.SuccessIcon(), opts.Dir)
	}
	return nil
}

// loadComponent runs the component's consumption-time loader — the same
// front door `clawker build` / `clawker monitor up` go through — so an
// author catches a manifest that parses structurally but breaks at
// consumption before publishing.
func loadComponent(c bundle.Component) error {
	var err error
	switch c.Type {
	case bundle.ComponentHarness:
		_, err = bundler.LoadBundle(c.Address.Name, c.FS)
	case bundle.ComponentStack:
		_, err = bundler.LoadStackDefinition(c.Address.Name, c.FS)
	case bundle.ComponentMonitoring:
		_, err = monitor.LoadMonitoringUnit(c.Address.Name, c.FS)
	default:
		return fmt.Errorf("component %s: unknown component type", c.Address.Name)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.Dir, err)
	}
	return nil
}
