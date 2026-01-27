// Package inspect provides the volume inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the inspect command.
type Options struct {
	// Format is reserved for future Go template support
}

// NewCmd creates the volume inspect command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "inspect VOLUME [VOLUME...]",
		Short: "Display detailed information on one or more volumes",
		Long: `Returns low-level information about clawker volumes.

Outputs detailed volume information in JSON format.`,
		Example: `  # Inspect a volume
  clawker volume inspect clawker.myapp.ralph-workspace

  # Inspect multiple volumes
  clawker volume inspect clawker.myapp.ralph-workspace clawker.myapp.ralph-config`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	return cmd
}

func run(f *cmdutil2.Factory, _ *Options, volumes []string) error {
	ctx := context.Background()
	ios := f.IOStreams

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	var results []any
	var errs []error

	for _, name := range volumes {
		// Inspect the volume
		vol, err := client.VolumeInspect(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect volume %q: %w", name, err))
			continue
		}

		results = append(results, vol)
	}

	// Output results
	if len(results) > 0 {
		if err := outputJSON(ios.Out, results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			cmdutil2.HandleError(ios, e)
		}
		return fmt.Errorf("failed to inspect %d volume(s)", len(errs))
	}

	return nil
}

func outputJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
