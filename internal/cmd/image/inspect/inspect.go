// Package inspect provides the image inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)

	Images []string
}

// NewCmdInspect creates the image inspect command.
func NewCmdInspect(f *cmdutil.Factory, runF func(context.Context, *InspectOptions) error) *cobra.Command {
	opts := &InspectOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

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
			opts.Images = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return inspectRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func inspectRun(ctx context.Context, opts *InspectOptions) error {
	ios := opts.IOStreams

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	var results []any
	var errs []error

	for _, ref := range opts.Images {
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
		if err := outputJSON(ios.Out, results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			cmdutil.HandleError(ios, e)
		}
		return fmt.Errorf("failed to inspect %d image(s)", len(errs))
	}

	return nil
}

func outputJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
