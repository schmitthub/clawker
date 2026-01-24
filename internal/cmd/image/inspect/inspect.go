// Package inspect provides the image inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/output"
	"github.com/spf13/cobra"
)

// Options holds options for the inspect command.
type Options struct {
	// Format is reserved for future Go template support
}

// NewCmd creates the image inspect command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "inspect IMAGE [IMAGE...]",
		Short: "Display detailed information on one or more images",
		Long: `Returns low-level information about clawker images.

Outputs detailed image information in JSON format.`,
		Example: `  # Inspect an image
  clawker image inspect clawker-myapp:latest

  # Inspect multiple images
  clawker image inspect clawker-myapp:latest clawker-backend:latest`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	return cmd
}

func run(f *cmdutil.Factory, _ *Options, images []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		output.HandleError(err)
		return err
	}

	var results []any
	var errs []error

	for _, ref := range images {
		// Inspect the image
		info, err := client.ImageInspect(ctx, ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect image %q: %w", ref, err))
			continue
		}

		results = append(results, info)
	}

	// Output results
	if len(results) > 0 {
		if err := outputJSON(results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			output.HandleError(e)
		}
		return fmt.Errorf("failed to inspect %d image(s)", len(errs))
	}

	return nil
}

func outputJSON(data any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
