// Package inspect provides the volume inspect command.
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
	Client    func(ctx context.Context) (*docker.Client, error)

	Names []string
}

// NewCmdInspect creates the volume inspect command.
func NewCmdInspect(f *cmdutil.Factory, runF func(context.Context, *InspectOptions) error) *cobra.Command {
	opts := &InspectOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

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
			opts.Names = args
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

	for _, name := range opts.Names {
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
			cmdutil.HandleError(ios, e)
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
